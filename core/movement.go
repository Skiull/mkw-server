package core

import (
	"fmt"
	"math"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
	"mkw-server/logging"
)

// Movement checks. Purely passive: the server only watches each player's reported positions
// and never corrects, delays or rubber-bands anyone, so legit movement is untouched.
//
// Time comes from the game's own frame counter in RACEHEADER_1 (frames since race start),
// so network jitter and lag spikes don't look like speed. Speeds are in course units per
// frame (u/f): normal driving is capped at 120 u/f by the game, Mushrooms and boost panels
// run at about 115, Bullet Bill at about 145 (MKWii TAS wiki, community guides).

const (
	// Average speed over this many frames is checked. Long enough to smooth out bumps,
	// knockbacks and moving platforms, short enough to catch a speed hack quickly.
	moveWindowFrames = 30
	// Highest legit average speed, with slack over the game's 120 u/f cap.
	maxDriveSpeed = 140
	// Highest legit average speed while in a Bullet Bill.
	maxBulletSpeed = 175
	// Highest legit average speed in a Mega Mushroom. Mega multiplies the 120 u/f cap by the
	// vehicle's Mega scale stat, 1.6 to 2.6 (MKWii TAS wiki, Velocity), so up to 312 u/f.
	maxMegaSpeed = 340
	// Slack for the per-vehicle Mega limit (gravity and conveyors add speed on top).
	megaSlack      = 1.15
	megaExtraSpeed = 10
	// How long a Mega is treated as active after it's used (it lasts about 7.5 s; slack for
	// lag between the item being used and the packet showing it).
	megaDuration = 12 * time.Second
	// Average speed that shows a Bullet Bill is flying, and speed below which it has ended.
	bulletMinSpeed = 130
	bulletEndSpeed = 122
	// Longest a Bullet Bill is treated as active after it's used.
	bulletFrames = 10 * 60
	// Cannons launch players far and fast; skip checks for this long after one.
	cannonFrames = 8 * 60
	// A single jump this large between two packets, outside a respawn or cannon, is a teleport.
	// The jump must also be well over what the speed limit allows for the frames in between,
	// so a lag gap between packets doesn't count.
	teleportDistance = 3000
	// Movement violations in one race before the player is kicked for cheating.
	movementViolationsBeforeKick = 1
	// Failures closer together than this count as one violation (and are logged once).
	moveLogInterval = 2 * time.Second
)

type moveSample struct {
	frame uint32
	pos   [3]float32
}

type moveState struct {
	samples     []moveSample
	cannonFlips byte
	bulletFlip  byte
	exemptUntil uint32 // Cannon flight: no checks until this frame.
	bulletUntil uint32
	bulletSeen  bool // Bullet Bill speed seen since the last Bullet Bill use.
	bulletEnded bool // Back to driving speed since; may still turn out to be a wall bump.
	hasFlags    bool
	maxSpeed    float32 // Fastest average seen this race, for tuning the limits.
	lastLog     time.Time
	violations  int // Movement violations this race.
	lastSpeed   float32

	// Offroad check (see offroad.go).
	floors                                *items.Floors
	stats                                 *items.Stats
	vehicle                               common.Vehicle
	character                             common.Character
	starFlip, megaFlip, mtFlip, trickFlip byte
	thwompFlip                            byte
	lastSquishLog                         time.Time
	squishSince                           uint32 // Frame a Thwomp squished the player, 0 if never.
	squishNote                            string // Log-only squish violation for trackMovement.
	comboNote                             string // Combo speed violation for trackMovement.
	course                                *items.Course
	driftSince                            uint32 // Frame the current drift started, 0 if not drifting.
	lastComboLog                          time.Time
	boostUntil                            uint32 // A boost may still be lifting the speed until this frame.
	offroadSince                          uint32 // Start of the current offroad stretch, 0 if not on offroad.
	offroadType                           uint8  // Lightest offroad in the current stretch.
}

// checkMovement looks at one RaceData record. It returns a violation, or "" if the movement
// looks legit. Caller must hold mu.
// walls may be nil (course collision unknown).
// mega is whether the player may be in a Mega Mushroom.
func checkMovement(p *Player, frame uint32, rd *records.RaceData, walls *items.Walls, mega bool) string {
	m := &p.move
	if m.hasFlags {
		if rd.CannonFlips() != m.cannonFlips {
			m.exemptUntil = frame + cannonFrames
		}
		if rd.BulletBillFlip() != m.bulletFlip {
			m.bulletUntil, m.bulletSeen, m.bulletEnded = frame+bulletFrames, false, false
		}
		if rd.StarFlip() != m.starFlip || rd.MegaFlip() != m.megaFlip || rd.BulletBillFlip() != m.bulletFlip {
			// A Star, Mega or Bullet Bill ends a squish: full speed again.
			m.squishSince = 0
		}
		if rd.StarFlip() != m.starFlip || rd.MegaFlip() != m.megaFlip {
			m.boostUntil = max(m.boostUntil, frame+powerItemFrames)
		}
		if rd.ThwompFlip() != m.thwompFlip {
			m.squishSince = frame
		}
		if rd.MiniTurboFlip() != m.mtFlip {
			// A super mini-turbo lasts up to 3x the vehicle's mini-turbo stat.
			m.boostUntil = max(m.boostUntil, frame+miniTurboFrames)
		}
		if rd.TrickFlip() != m.trickFlip {
			m.boostUntil = max(m.boostUntil, frame+boostFrames)
		}
	}
	m.cannonFlips, m.bulletFlip, m.hasFlags = rd.CannonFlips(), rd.BulletBillFlip(), true
	m.starFlip, m.megaFlip, m.mtFlip, m.trickFlip = rd.StarFlip(), rd.MegaFlip(), rd.MiniTurboFlip(), rd.TrickFlip()
	m.thwompFlip = rd.ThwompFlip()
	// A drift held for half a second or more ends in a mini-turbo (no reliable RACEDATA bit
	// marks one, so the drift state is used: buttons 7/15 while drifting).
	drifting := rd.Buttons == 7 || rd.Buttons == 15
	if drifting && m.driftSince == 0 {
		m.driftSince = frame
	} else if !drifting && m.driftSince != 0 {
		if frame-m.driftSince >= miniTurboMinDrift {
			m.boostUntil = max(m.boostUntil, frame+miniTurboFrames)
		}
		m.driftSince = 0
	}
	if rd.MushroomBoost() {
		m.boostUntil = max(m.boostUntil, frame+boostFrames)
	}

	if !rd.HasPosition() {
		return ""
	}
	pos := rd.WorldPos()
	if n := len(m.samples); n > 0 && frame <= m.samples[n-1].frame {
		return "" // Same or older frame: duplicate or reordered packet.
	}
	// Respawns and cannons move players in ways that aren't driving: start over.
	if rd.Respawning() || frame < m.exemptUntil {
		m.samples = m.samples[:0]
		return ""
	}

	var problem string
	if n := len(m.samples); n > 0 {
		last := m.samples[n-1]
		if d := items.Distance(last.pos, pos); d > teleportDistance && d > 2*speedLimit(frame, m, mega)*float32(frame-last.frame) {
			problem = fmt.Sprintf("jumped %.0f units in %d frames", d, frame-last.frame)
			m.samples = m.samples[:0]
		} else if frame >= m.bulletUntil {
			// Bullet Bill follows its own path and can clip scenery, so it's skipped.
			if tri, hit := walls.Crosses(last.pos, pos); hit {
				problem = fmt.Sprintf("passed through wall triangle %d between (%.0f, %.0f, %.0f) and (%.0f, %.0f, %.0f)",
					tri, last.pos[0], last.pos[1], last.pos[2], pos[0], pos[1], pos[2])
			}
		}
	}
	m.samples = append(m.samples, moveSample{frame, pos})
	// A leaf pile's Mushroom may not show as a boost: count a recent pile as one.
	trackOffroad(m, frame, pos, mega || within(p.leafPileAt, 3*time.Second, time.Now()))

	cut := 0
	for cut < len(m.samples)-1 && frame-m.samples[cut+1].frame >= moveWindowFrames {
		cut++
	}
	m.samples = m.samples[cut:]

	if first := m.samples[0]; problem == "" && frame-first.frame >= moveWindowFrames {
		speed := items.Distance(first.pos, pos) / float32(frame-first.frame)
		m.maxSpeed = max(m.maxSpeed, speed)
		m.lastSpeed = speed
		// A Bullet Bill flies at about 145 u/f, over the 120 cap for driving, and ends
		// whenever the game decides: sooner when used near the front, or early if it gets
		// stuck on a wall. Once the speed has been bullet speed and drops back to driving
		// speed it counts as over, but only provisionally: a bill that bumps a wall and
		// flies on reaches bullet speed again and counts as active again.
		if frame < m.bulletUntil {
			switch {
			case speed >= bulletMinSpeed:
				m.bulletSeen, m.bulletEnded, p.bulletEnded = true, false, false
			case m.bulletSeen && speed < bulletEndSpeed:
				m.bulletEnded, p.bulletEnded = true, true
			}
		}
		// Horizontal speed only (falling adds vertical speed), and not in the air.
		hspeed := float32(math.Hypot(float64(pos[0]-first.pos[0]), float64(pos[2]-first.pos[2]))) / float32(frame-first.frame)
		// Going downhill, gravity adds speed past the engine's top speed (up to the 120 cap).
		descent := (first.pos[1] - pos[1]) / float32(frame-first.frame)
		if limit, ok := comboSpeedLimit(m, frame, mega); ok && hspeed > limit && descent <= maxComboDescent && !airborne(m.floors, pos) && !airborne(m.floors, first.pos) && !onStickyRoad(m.floors, pos) && !onStickyRoad(m.floors, first.pos) && !(m.course != nil && m.course.NearBelt(pos)) {
			m.comboNote = fmt.Sprintf("averaged %.0f u/f over %d frames with no boost (combo limit %.0f)", hspeed, frame-first.frame, limit)
		}
		if limit, ok := squishLimit(m, first.frame, frame); ok && !mega && !(frame < m.bulletUntil && !m.bulletEnded) && speed > limit {
			m.squishNote = fmt.Sprintf("averaged %.0f u/f over %d frames while squished (limit %.0f)", speed, frame-first.frame, limit)
		}
		if limit, ok := offroadLimit(m, first.frame); ok && speed > limit {
			return fmt.Sprintf("averaged %.0f u/f over %d frames on %s (limit %.0f)", speed, frame-first.frame, floorName(m.offroadType), limit)
		}
		if limit := speedLimit(frame, m, mega); speed > limit {
			problem = fmt.Sprintf("averaged %.0f u/f over %d frames (limit %.0f)", speed, frame-first.frame, limit)
		}
	}
	return problem
}

func speedLimit(frame uint32, m *moveState, mega bool) float32 {
	switch {
	case mega:
		return megaSpeedLimit(m)
	case frame < m.bulletUntil && !m.bulletEnded:
		return maxBulletSpeed
	default:
		return maxDriveSpeed
	}
}

// megaSpeedLimit is the 120 u/f cap times the vehicle's Mega scale, with slack. Without
// stats, the largest Mega scale of any vehicle is assumed.
func megaSpeedLimit(m *moveState) float32 {
	scale := m.stats.MegaScale(m.vehicle)
	if scale == 0 {
		return maxMegaSpeed
	}
	return 120*scale*megaSlack + megaExtraSpeed
}

// trackMovement runs the movement check on a packet's RaceData. Caller must hold mu.
func trackMovement(a *Aid, pkt *RacePacket, now time.Time) {
	if !room.config.CheckMovement || pkt.RaceInfo == nil || !a.raceStarted || a.mode != common.ModeVS && a.mode != common.ModeBalloonBattle && a.mode != common.ModeCoinRunners {
		return
	}
	frame := pkt.RaceInfo.Frame()
	for i, rd := range pkt.RaceData {
		if i >= len(a.players) {
			break
		}
		p := a.players[i]
		var walls *items.Walls
		if c := room.config.Courses[a.course]; c != nil {
			walls = c.Walls
		}
		mega := !p.megaAt.IsZero() && now.Sub(p.megaAt) <= megaDuration
		p.move.floors, p.move.course = nil, nil
		if c := room.config.Courses[a.course]; c != nil {
			p.move.floors, p.move.course = c.Floors, c
		}
		p.move.stats, p.move.vehicle, p.move.character = room.config.Stats, p.vehicle, p.character
		problem := checkMovement(p, frame, rd, walls, mega)
		recordPos(a, i, p, rd, now)
		trackShrinkSpeed(a, p, now)
		if note := p.move.comboNote; note != "" {
			p.move.comboNote = ""
			if room.config.EnforceMovement {
				if movementViolation(a, p, i, "combo speed: "+note, now) {
					return
				}
				continue
			}
			if now.Sub(p.move.lastComboLog) >= moveLogInterval {
				p.move.lastComboLog = now
				logging.Log("Aid %d player %d combo speed check failed (not enforced): %s", a.aid, i, note)
				recordFlag(a, "combo speed (not enforced): "+note, now)
			}
		}
		if note := p.move.squishNote; note != "" {
			p.move.squishNote = ""
			if room.config.EnforceMovement {
				if movementViolation(a, p, i, note, now) {
					return
				}
				continue
			}
			if now.Sub(p.move.lastSquishLog) >= moveLogInterval {
				p.move.lastSquishLog = now
				logging.Log("Aid %d player %d squish check failed (not enforced): %s", a.aid, i, note)
			}
		}
		observeHits(a, p, rd, now)
		if problem == "" {
			continue
		}
		if movementViolation(a, p, i, problem, now) {
			return
		}
	}
}

// movementViolation counts a failed movement check and kicks the aid on the third
// violation in a race when enforced. It reports whether the aid was kicked.
// Caller must hold mu.
func movementViolation(a *Aid, p *Player, i int, problem string, now time.Time) bool {
	// One impossible move fails several packets in a row (the speed average stays high
	// for a while), so failures this close together count as one violation.
	if now.Sub(p.move.lastLog) < moveLogInterval {
		return false
	}
	p.move.lastLog = now
	p.move.violations++
	if !room.config.EnforceMovement {
		logging.Log("Aid %d player %d movement check failed (not enforced): %s", a.aid, i, problem)
		return false
	}
	logging.Log("Aid %d player %d movement violation %d/%d this race: %s", a.aid, i, p.move.violations, movementViolationsBeforeKick, problem)
	recordFlag(a, "movement violation: "+problem, now)
	if p.move.violations >= movementViolationsBeforeKick {
		kick(a, KickCheating)
		return true
	}
	return false
}

// resetMovement starts a new race's movement state, logging the last race's top speed.
// Caller must hold mu.
func resetMovement(a *Aid) {
	for i, p := range a.players {
		if p.move.maxSpeed > 0 {
			logging.Log("Aid %d player %d top average speed last race: %.0f u/f", a.aid, i, p.move.maxSpeed)
		}
		p.move = moveState{}
	}
}

// Squish (Thwomp or Toad's Factory press): speed is multiplied by 0.7 for at least 240
// frames (Kinoko KartMove/KartAction: crush timer 240 or 480 plus a 30 or 90 frame action;
// which applies to which press isn't named). Only the shortest certain stretch is checked,
// after the speed has had time to drop. Mega squishes don't show in RACEDATA.
const (
	squishFrames      = 240
	squishSettle      = 30
	squishSpeedFactor = 0.7
)

func squishLimit(m *moveState, windowStart, frame uint32) (float32, bool) {
	if m.squishSince == 0 || windowStart < m.squishSince+squishSettle || frame > m.squishSince+squishFrames {
		return 0, false
	}
	return 120*squishSpeedFactor*offroadSlack + offroadExtraSpeed, true
}

// Per-combo top speed (a movement violation with -enforce-movement). Without a boost, a vehicle can't go faster than
// its base speed stat (vehicle + character, kartParam/driverParam 0x18) x 1.15, plus 15% for a
// bike's wheelie (Kinoko KartMove). Boosts lift it: Mushroom/boost panel x1.4, mini-turbo
// x1.2, trick x1.3, and Star/Mega/Bullet Bill, all excluded via boostUntil and friends.
// Slipstream (drafting) isn't visible in RACEDATA; legit races showed none over the limit.
const (
	wheelieBonus    = 0.15
	comboSpeedSlack = 1.10
	comboSpeedExtra = 5
	comboAfterBoost = 60
	// Full speed is the base stat times this (measured: 150cc top speed in test races was
	// 1.15x the stat for karts).
	engineSpeedFactor = 1.15
	miniTurboMinDrift = 30
	maxComboDescent   = 2 // u/f of height lost; steeper and the check is skipped.
	miniTurboFrames   = 200
)

func comboSpeedLimit(m *moveState, frame uint32, mega bool) (float32, bool) {
	// Speed falls back gradually after a boost: wait a second after one ends.
	if m.stats == nil || frame < m.boostUntil+comboAfterBoost || mega || (frame < m.bulletUntil && !m.bulletEnded) {
		return 0, false
	}
	base := m.stats.BaseSpeed(m.vehicle, m.character)
	if base == 0 {
		return 0, false
	}
	mul := float32(1)
	if items.IsBike(m.vehicle) {
		mul += wheelieBonus
	}
	return base*engineSpeedFactor*mul*comboSpeedSlack + comboSpeedExtra, true
}

// onStickyRoad: floors that add speed the combo limit doesn't model: sticky road (KCL
// 0x16, banked mine tracks on Wario's Gold Mine) keeps speed through slopes and turns, and
// moving and rotating roads (conveyors, escalators) add their own speed to the player's.
// Toad's Factory's belts are objects, not KCL: near them it's skipped too.
func onStickyRoad(f *items.Floors, pos [3]float32) bool {
	ty, ok := f.At(pos)
	return ok && (ty == items.FloorStickyRoad || ty == items.FloorMovingRoad || ty == items.FloorRotatingRoad)
}
