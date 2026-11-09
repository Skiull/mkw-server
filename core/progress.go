package core

import (
	"fmt"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
	"mkw-server/logging"
)

// Server-side race progress. Each player is followed through the course's checkpoint zones
// (KMP CKPT/CKPH) with the game's lap rules, so the server knows everyone's lap and race
// completion without trusting the rank clients report. See docs/movement-checks.md.

const (
	// After Lakitu, the player must reappear this close (horizontally) to the respawn point
	// for their last checkpoint.
	respawnRadius = 1500
	// A rank disagreement must last this long before it's logged, since clients and the
	// server see positions at slightly different times.
	rankMismatchGrace = 3 * time.Second
	rankLogInterval   = 10 * time.Second
)

type progressState struct {
	cp     int // Current checkpoint, -1 until the player is first placed in a zone.
	lap    int
	maxKey int // Highest key checkpoint reached this lap.
	// Lap + lap completion: what players are ranked by.
	completion float32
	tracked    bool

	respawning bool
	respawnCP  int // Checkpoint the player was in when the respawn started.

	mismatchSince time.Time
	lastRankLog   time.Time

	// Game frame (RACEHEADER_1) when the server saw the player reach the final lap count.
	finishFrame uint32
	// The client has reported finishing; checked once.
	finishSeen bool
}

// Laps to finish: everyone starts on lap 0 behind the line, crossing it makes lap 1, and the
// race ends on reaching lap 4.
const finishLap = 4

// The race clock in RACEHEADER_1 runs this far ahead of the on-screen timer (it includes the
// countdown). Measured from real finishes: 3.79-3.93s; the spread is the time between
// position updates.
const countdownOffset = 3900 * time.Millisecond

// A claimed finish time may be this much faster than the server's clock allows.
const finishTimeSlack = 500 * time.Millisecond

func newProgress() progressState {
	return progressState{cp: -1, respawnCP: -1}
}

// pathBetween returns the checkpoints passed going from "from" to "to" within a few steps
// forward (dir > 0) or backward (dir < 0), "to" included, or nil if it's not that close.
func pathBetween(cps *items.Checkpoints, from, to int, dir int) []int {
	parent := map[int]int{from: -1}
	frontier := []int{from}
	for range 6 {
		var next []int
		for _, i := range frontier {
			links := cps.List[i].Next
			if dir < 0 {
				links = cps.List[i].Prev
			}
			for _, j := range links {
				if _, seen := parent[j]; seen {
					continue
				}
				parent[j] = i
				if j == to {
					var path []int
					for k := j; k != from; k = parent[k] {
						path = append([]int{k}, path...)
					}
					return path
				}
				next = append(next, j)
			}
		}
		frontier = next
	}
	return nil
}

// updateProgress follows a player through the checkpoints and checks respawn locations.
// It returns a respawn problem, or "". Caller must hold mu.
func updateProgress(p *Player, c *items.Course, rd *records.RaceData, frame uint32) string {
	st := &p.progress
	if c == nil || !rd.HasPosition() {
		return ""
	}
	pos := rd.WorldPos()

	// Respawns: remember where the player was when it started, check where they land.
	var problem string
	if rd.Respawning() {
		if !st.respawning {
			st.respawning, st.respawnCP = true, st.cp
		}
		return ""
	}
	if st.respawning {
		st.respawning = false
		problem = checkRespawn(c, st.respawnCP, pos)
	}

	cps := c.Checkpoints
	if cps == nil {
		return problem
	}
	cp, t, ok := cps.Locate([2]float32{pos[0], pos[2]}, st.cp)
	if !ok {
		return problem // Off every zone (in the air, out of bounds): keep the last one.
	}

	// Positions arrive every few frames, so at speed a thin zone can be skipped between two
	// of them: every checkpoint along the way counts as passed, in order.
	passed := []int{cp}
	backward := false
	switch {
	case st.cp < 0 || cp == st.cp:
		// First placement (the start grid is just behind the lap counter, in lap 0), or no move.
	default:
		if path := pathBetween(cps, st.cp, cp, 1); path != nil {
			passed = path
		} else if path := pathBetween(cps, st.cp, cp, -1); path != nil {
			passed, backward = path, true
		}
	}
	prev := st.cp
	for _, i := range passed {
		switch {
		case backward && prev == cps.LapCounter:
			// Backwards over the line takes the lap away again.
			st.lap--
			st.maxKey = cps.MaxKey
		case !backward && i == cps.LapCounter && prev != i && prev >= 0:
			// Crossing the line counts if every key checkpoint was reached this lap (the
			// first crossing, from the grid, always counts).
			if st.lap == 0 || st.maxKey >= cps.MaxKey {
				st.lap++
				st.maxKey = 0
				if st.lap == finishLap && st.finishFrame == 0 {
					st.finishFrame = frame
				}
			}
		}
		// Key checkpoints count when reached, in order or ahead (the game allows ultra
		// shortcuts that reach the last key checkpoint, so the server does too).
		if k := cps.List[i].Type; !backward && k > 0 && k > st.maxKey {
			st.maxKey = k
		}
		prev = i
	}
	st.cp = cp
	st.tracked = true
	st.completion = float32(st.lap) + cps.List[cp].Completion + t*cps.List[cp].Span
	return problem
}

// checkRespawn checks a player landed at the respawn point for the checkpoint they were in
// (or one next to it, since the server's view of the checkpoint can be a little behind).
func checkRespawn(c *items.Course, cp int, pos [3]float32) string {
	if len(c.Respawns) == 0 {
		return ""
	}
	var candidates []int
	if cps := c.Checkpoints; cps != nil && cp >= 0 {
		candidates = append(candidates, cps.List[cp].Respawn)
		for _, j := range append(append([]int{}, cps.List[cp].Next...), cps.List[cp].Prev...) {
			candidates = append(candidates, cps.List[j].Respawn)
		}
	} else {
		// Battle arenas (no checkpoints) or never placed: any respawn point.
		for i := range c.Respawns {
			candidates = append(candidates, i)
		}
	}
	nearest := float32(-1)
	for _, r := range candidates {
		if r < 0 || r >= len(c.Respawns) {
			continue
		}
		pt := c.Respawns[r]
		d := items.Distance([3]float32{pt[0], 0, pt[2]}, [3]float32{pos[0], 0, pos[2]})
		if d <= respawnRadius {
			return ""
		}
		if nearest < 0 || d < nearest {
			nearest = d
		}
	}
	return fmt.Sprintf("respawned %.0f units from the respawn point for checkpoint %d", nearest, cp)
}

// rankSlack is how far behind (in laps) another racer can be and still plausibly be ahead
// of the player in the player's own game: lag and photo finishes. About 2s on a typical lap.
const rankSlack = 0.05

// worstPlausibleRank is the worst rank a player can honestly claim: 1 + every racer ahead of
// them or within rankSlack behind. ok is false when some racer isn't tracked yet.
// Caller must hold mu.
func worstPlausibleRank(p *Player, course common.Course) (int, bool) {
	if !p.progress.tracked || p.progressCourse != course {
		return 0, false
	}
	mine := p.progress.completion
	worst := 1
	for _, a := range aids {
		if a.kicked || !(a.isRacer || a.bot != nil) {
			continue
		}
		for _, q := range a.players {
			if q == p {
				continue
			}
			if !q.progress.tracked || q.progressCourse != course {
				return 0, false
			}
			if q.progress.completion >= mine-rankSlack {
				worst++
			}
		}
	}
	return worst, true
}

// itemRank is the rank item odds use: the client's own rank (it sees the race as the player
// does), but never worse than the server says is plausible, so claiming last place to get
// better items doesn't work. Caller must hold mu.
func itemRank(p *Player, course common.Course) (rank int, capped bool) {
	rank = int(p.rank)
	worst, ok := worstPlausibleRank(p, course)
	if !ok {
		return rank, false
	}
	if rank == 0 || rank > worst {
		return worst, rank != 0
	}
	return rank, false
}

// trackProgress updates a player's progress from a RaceData record and logs respawn and
// rank problems. Caller must hold mu.
func trackProgress(a *Aid, idx int, rd *records.RaceData, course common.Course, frame uint32, now time.Time) {
	c := room.config.Courses[course]
	p := a.players[idx]
	if p.progressCourse != course {
		p.progress, p.progressCourse = newProgress(), course
	}
	if problem := updateProgress(p, c, rd, frame); problem != "" && a.bot == nil {
		if room.config.KickRespawnCheats {
			logging.Log("Aid %d player %d respawn cheat: %s", a.aid, idx, problem)
			kick(a, KickCheating)
		} else if room.config.EnforceMovement {
			strike(a, "movement: "+problem, now)
		} else {
			logging.Log("Aid %d player %d respawn check failed (not enforced): %s", a.aid, idx, problem)
		}
	}
	if a.bot != nil || a.mode != common.ModeVS {
		return
	}

	// Log clients claiming a worse rank than is plausible, once it's lasted a while.
	st := &p.progress
	_, capped := itemRank(p, course)
	if !capped {
		st.mismatchSince = time.Time{}
		return
	}
	if st.mismatchSince.IsZero() {
		st.mismatchSince = now
	}
	if now.Sub(st.mismatchSince) >= rankMismatchGrace && now.Sub(st.lastRankLog) >= rankLogInterval {
		st.lastRankLog = now
		worst, _ := worstPlausibleRank(p, course)
		logging.Log("Aid %d player %d claims rank %d, worst plausible is %d (lap %d, %.2f)",
			a.aid, idx, p.rank, worst, st.lap, st.completion)
	}
}

// resetProgress starts a new race. Caller must hold mu.
func resetProgress(a *Aid) {
	for _, p := range a.players {
		p.progress = newProgress()
	}
}

// checkFinish compares a client's claim to have finished with the server's lap count. A
// finish is accepted if the server has the player on the final lap count, or within a
// packet of it (the line is crossed between two position updates). Caller must hold mu.
func checkFinish(a *Aid, pkt *RacePacket, now time.Time) {
	if pkt.RaceMode == nil || pkt.RaceMode.VS == nil || pkt.RaceInfo == nil {
		return
	}
	c := room.config.Courses[a.course]
	if c == nil || c.Checkpoints == nil {
		return
	}
	vs := pkt.RaceMode.VS
	for i := range min(int(vs.LocalPlayerCount), len(a.players)) {
		ft := vs.FinishTimes[i]
		if ft == (records.FinishTime{}) {
			continue
		}
		p := a.players[i]
		st := &p.progress
		if st.finishSeen || p.progressCourse != a.course {
			continue
		}
		st.finishSeen = true

		claimed := time.Duration(ft.Minutes)*time.Minute + time.Duration(ft.Seconds)*time.Second +
			time.Duration(ft.Milliseconds)*time.Millisecond
		var problem string
		if finishOK(st) {
			raceHadFinisher = true
		}
		switch {
		case !finishOK(st) && someoneElseFinished(p):
			// Once someone has crossed the line the game ends the race for everyone still
			// driving (time's up) and marks them finished too. Not a cheat.
			logging.Log("Aid %d player %d finished on lap %d (%.2f): race ended by an earlier finisher", a.aid, i, st.lap, st.completion)
		case !finishOK(st):
			problem = fmt.Sprintf("claims to have finished, but the server has them on lap %d (%.2f)", st.lap, st.completion)
		case st.finishFrame != 0:
			if earliest := finishEarliest(st.finishFrame); claimed < earliest {
				problem = fmt.Sprintf("claims %v, but the race clock allows %v at the fastest", claimed, earliest.Round(time.Millisecond))
			}
		}
		if problem == "" {
			continue
		}
		// Checked once per race, at the finish, so one failure kicks.
		if room.config.EnforceMovement {
			logging.Log("Aid %d player %d finish cheat: %s", a.aid, i, problem)
			kick(a, KickCheating)
			return
		}
		logging.Log("Aid %d player %d finish check failed (not enforced): %s", a.aid, i, problem)
	}
}

// someoneElseFinished reports whether another player in the room has really finished this
// race (the server saw them complete it). Caller must hold mu.
// raceHadFinisher is set once anyone legitimately finishes this race, and stays set if they
// leave the room afterwards. Cleared at each race's countdown. Guarded by mu.
var raceHadFinisher bool

func someoneElseFinished(p *Player) bool {
	if raceHadFinisher {
		return true
	}
	for _, a := range aids {
		for _, q := range a.players {
			if q != p && q.progress.finishSeen && finishOK(&q.progress) {
				return true
			}
		}
	}
	return false
}

// finishOK reports whether the server agrees the player can have finished.
func finishOK(st *progressState) bool {
	return st.lap >= finishLap || st.completion >= finishLap-0.05
}

// finishEarliest is the fastest finish time a player who reached the final lap at frame can
// claim.
func finishEarliest(frame uint32) time.Duration {
	return time.Duration(frame)*time.Second/60 - countdownOffset - finishTimeSlack
}
