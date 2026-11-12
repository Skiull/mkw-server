package core

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
	"mkw-server/logging"
)

// Green Shell paths (log only, kinoko/docs/thrown-items.md). Each thrown Green Shell is
// simulated in Kinoko (`kinoko items`, from the AI bot folder) from its Shot record; the
// path, frame by frame from the throw, is lined up with players' positions by arrival time.
// A player the path passes through should get hit: that's logged as a hit expectation, never
// kicked on. The path stops where the owner reports the shell destroyed (action-4 0xC1) or a
// player reports being hit by it (0x02). Item-item collisions and course objects aren't
// simulated, so the expectations are only for measuring how often the path is right.

const (
	shellKartRadius   = 100 // Kart body, added to the shell's 34 x scale.
	shellHitWait      = 1500 * time.Millisecond
	shellSpawnAge     = 3 // A shell's age on its throw frame.
	maxShellPaths     = 32
	shellSimTimeout   = 10 * time.Second
	shellRepeatPeriod = 2 * time.Second // One expectation per shell and player.
)

type shellPoint struct {
	pos       [3]float32
	radius    float32 // Against the course.
	hitRadius float32
}

type shellPath struct {
	owner   *Aid
	idx     byte // Owner's player index and item counter, as in action-4 reports.
	counter byte
	frame   uint16 // Race frame of the throw.
	at      time.Time
	points  []shellPoint // By age - shellSpawnAge.
	end     string
	// Set once the path is cut by a report: points after this index never happened.
	cut      int
	expected map[*Player]time.Time
}

// Guarded by mu.
var shellPaths []*shellPath

// simulateShell starts a Kinoko run for a thrown Green Shell. data is its 20-byte Shot slot.
// Caller must hold mu.
func simulateShell(a *Aid, s *records.Shot, data []byte, now time.Time) {
	dir := room.config.AIBotDir
	if dir == "" || s.Object != common.ObjGreenShell || len(data) < 20 {
		return
	}
	course := a.course
	go func() {
		points, end, err := runKinokoItems(dir, course, data[:20], common.ObjGreenShell)
		if err != nil {
			logging.Log("Aid %d Green Shell simulation failed: %v", a.aid, err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(shellPaths) >= maxShellPaths {
			shellPaths = shellPaths[1:]
		}
		shellPaths = append(shellPaths, &shellPath{owner: a, idx: s.Owner, counter: s.Counter, frame: s.Frame,
			at: now, points: points, end: end, cut: len(points), expected: map[*Player]time.Time{}})
		logging.Log("Aid %d Green Shell (player %d, item %d, frame %d): simulated %d frames, ends %s",
			a.aid, s.Owner, s.Counter, s.Frame, len(points), end)
	}()
}

func runKinokoItems(dir string, course common.Course, data []byte, obj common.ItemObject) ([]shellPoint, string, error) {
	in, err := os.CreateTemp("", "shot-*.txt")
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(in.Name())
	fmt.Fprintf(in, "s %s %d\n", hex.EncodeToString(data), obj)
	in.Close()
	outPath := in.Name() + ".out"
	defer os.Remove(outPath)

	cmd := exec.Command(filepath.Join(dir, "kinoko.exe"), "items", "-c", strconv.Itoa(int(course)), "-i", in.Name(), "-o", outPath)
	cmd.Dir = dir
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, "", err
		}
	case <-time.After(shellSimTimeout):
		cmd.Process.Kill()
		return nil, "", fmt.Errorf("timed out")
	}
	f, err := os.Open(outPath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return parseKinokoItems(bufio.NewScanner(f))
}

// parseKinokoItems reads `kinoko items` output for one shot:
// `<id> <age> <x> <y> <z> <course radius> <hit radius> <bounces> <wall hits>` per frame, then
// `<id> end <reason> <frames>`.
func parseKinokoItems(sc *bufio.Scanner) ([]shellPoint, string, error) {
	var points []shellPoint
	end := ""
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && f[1] == "end" {
			end = f[2]
			continue
		}
		if len(f) < 7 {
			continue
		}
		var v [5]float64
		for i := range v {
			x, err := strconv.ParseFloat(f[2+i], 32)
			if err != nil {
				return nil, "", fmt.Errorf("bad line %q", sc.Text())
			}
			v[i] = x
		}
		points = append(points, shellPoint{pos: [3]float32{float32(v[0]), float32(v[1]), float32(v[2])}, radius: float32(v[3]), hitRadius: float32(v[4])})
	}
	if len(points) == 0 {
		return nil, "", fmt.Errorf("no frames (end %q)", end)
	}
	return points, end, sc.Err()
}

// shellReport handles an EVENT action-4 slot: a shell destroyed (from its owner) or a hit
// (from its victim). Caller must hold mu.
func shellReport(a *Aid, r *records.ItemReport, now time.Time) {
	if r.Object != common.ObjGreenShell {
		return
	}
	for _, s := range shellPaths {
		if s.idx != r.Owner || s.counter != r.Counter {
			continue
		}
		if r.Code == records.ItemReportDestroyed && s.owner != a {
			continue
		}
		if r.Code != records.ItemReportDestroyed && r.Code != records.ItemReportHit {
			continue
		}
		// The report's frame is in the reporter's race; close enough to the owner's.
		if i := int(r.Frame) - int(s.frame); i >= 0 && i < s.cut {
			s.cut = i + 1
		}
		if r.Code == records.ItemReportHit {
			for _, e := range hitExpectations {
				if e.shell == s && e.aid == a {
					e.reported = true
				}
			}
			logging.Log("Aid %d reports a hit by aid %d's Green Shell (item %d) at frame %d", a.aid, s.owner.aid, s.counter, r.Frame)
		}
		return
	}
}

// shellIndex is the path index for a moment, by time since the throw arrived.
func (s *shellPath) index(now time.Time) int {
	return int(now.Sub(s.at).Seconds() * 60)
}

// checkShells expects a hit on p if a simulated shell is on it now. Caller must hold mu.
func checkShells(a *Aid, p *Player, now time.Time) {
	kept := shellPaths[:0]
	for _, s := range shellPaths {
		i := s.index(now)
		if i >= s.cut {
			continue // Over: dropped.
		}
		kept = append(kept, s)
		if s.owner == a || i < 0 || p.invincible(now) || now.Sub(p.respawnedAt) < 3*time.Second {
			continue
		}
		pt := s.points[i]
		d := items.Distance(pt.pos, p.lastPos)
		if d > pt.hitRadius+shellKartRadius || now.Sub(s.expected[p]) < shellRepeatPeriod {
			continue
		}
		s.expected[p] = now
		e := expectHit(a, p, fmt.Sprintf("simulated Green Shell from aid %d (item %d) %.0f units away at age %d",
			s.owner.aid, s.counter, d, i+shellSpawnAge), now, shellHitWait, false)
		e.logOnly = true
		e.shell = s
	}
	shellPaths = kept
}

func resetShellPaths() {
	shellPaths = nil
}
