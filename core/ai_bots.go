package core

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/logging"
)

// AI bots: instead of mirroring a real player, each bot is driven by its own process of our
// Kinoko fork (the MKW physics engine) in "serve" mode, whose AI follows a top racing line
// for the course. The process outputs the bot's RACEDATA record every frame; the bot sends
// the latest one. See host/BOTS.md.
//
// Timing: the game's RaceInfo counts frames since the countdown started. A bot's process is
// stepped so that its frame = its countdown start frame + the reference player's countdown
// frame (extrapolated by wall time), so bots race in step with the room.

// Line file prefix (Kinoko samples, <prefix>-...-rta-....line) per course, in course order.
var aiLinePrefixes = map[common.Course]string{
	common.MarioCircuit: "mc", common.MooMooMeadows: "mmm", common.MushroomGorge: "mg",
	common.GrumbleVolcano: "gv", common.ToadsFactory: "tf", common.CoconutMall: "cm",
	common.DKSummit: "dks", common.WariosGoldMine: "wgm", common.LuigiCircuit: "lc",
	common.DaisyCircuit: "dc", common.MoonviewHighway: "mh", common.MapleTreeway: "mt",
	common.BowsersCastle: "bc", common.RainbowRoad: "rr", common.DryDryRuins: "ddr",
	common.KoopaCape: "kc", common.RPeachBeach: "rpb", common.RMarioCircuit: "rmc",
	common.RWaluigiStadium: "rws", common.RDKMountain: "rdkm", common.RYoshiFalls: "ryf",
	common.RDesertHills: "rdh", common.RPeachGardens: "rpg", common.RDelfinoSquare: "rds",
	common.RMarioCircuit3: "rmc3", common.RGhostValley2: "rgv2", common.RMarioRaceway: "rmr",
	common.RSherbetLand: "rsl", common.RBowsersCastle: "rbc", common.RDKJungleParkway: "rdkjp",
	common.RBC3: "rbc3", common.RShyGuyBeach: "rsgb",
}

// Online, course objects start about 3 seconds later than in time trials.
const aiObjectDelayFrames = 180

// Bot combos: 90% Funky Kong + Flame Runner, 10% Daisy + Mach Bike.
const (
	aiFunkyKong   = 22
	aiFlameRunner = 23
	aiDaisy       = 15
	aiMachBike    = 22
)

// aiLineFor picks the course's racing line: no-glitch first, then no-shortcut, then the
// plain run (tracks without glitches only have that one).
func aiLineFor(dir string, course common.Course) string {
	prefix, ok := aiLinePrefixes[course]
	if !ok {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "lines", prefix+"-*.line"))
	sort.Strings(matches)
	pick := func(tag string) string {
		for _, m := range matches {
			base := filepath.Base(m)
			// "rmc-..." must not match "rmc3-...".
			if !strings.HasPrefix(base, prefix+"-") {
				continue
			}
			if tag == "" || strings.Contains(base, tag) {
				return m
			}
		}
		return ""
	}
	for _, tag := range []string{"-ng-", "-nosc-", "-rta-"} {
		if m := pick(tag); m != "" {
			return m
		}
	}
	return ""
}

// aiDriver runs one bot's Kinoko process for one race.
type aiDriver struct {
	mu        sync.Mutex
	record    []byte // Latest RACEDATA record.
	stage     int
	finished  bool
	finishAt  int // Frames from race start to the finish, once finished.
	frame     int
	countdown int // Kinoko frame where the countdown starts.
	raceStart int
	rankBit   int

	target chan int // Kinoko frame to simulate up to.
	cmd    *exec.Cmd
	done   chan struct{}
}

func startAIDriver(dir string, course common.Course, character, vehicle byte, slot, players int) (*aiDriver, error) {
	line := aiLineFor(dir, course)
	if line == "" {
		return nil, fmt.Errorf("no racing line for course %d", course)
	}
	exe := filepath.Join(dir, "kinoko.exe")
	args := []string{"serve", "-c", strconv.Itoa(int(course)), "-k", strconv.Itoa(int(character)),
		"-v", strconv.Itoa(int(vehicle)), "-l", line, "-p", strconv.Itoa(slot), "-n", strconv.Itoa(players),
		"-o", strconv.Itoa(aiObjectDelayFrames)}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	d := &aiDriver{cmd: cmd, target: make(chan int, 1), done: make(chan struct{})}
	in := bufio.NewReader(stdout)
	readLine := func() (string, error) {
		s, err := in.ReadString('\n')
		return strings.TrimSpace(s), err
	}

	if s, err := readLine(); err != nil || s != "ready" {
		cmd.Process.Kill()
		return nil, fmt.Errorf("kinoko serve didn't start (%q, %v)", s, err)
	}
	fmt.Fprintln(stdin, "info")
	s, err := readLine()
	f := strings.Fields(s)
	if err != nil || len(f) < 4 || f[0] != "info" {
		cmd.Process.Kill()
		return nil, fmt.Errorf("kinoko serve info: %q, %v", s, err)
	}
	d.countdown, _ = strconv.Atoi(f[1])
	d.raceStart, _ = strconv.Atoi(f[2])
	d.rankBit, _ = strconv.Atoi(f[3])

	go func() {
		defer close(d.done)
		defer cmd.Process.Kill()
		for target := range d.target {
			d.mu.Lock()
			n := target - d.frame
			d.mu.Unlock()
			if n <= 0 {
				continue
			}
			if n > 600 {
				n = 600 // Catch up in chunks.
			}
			if _, err := fmt.Fprintf(stdin, "step %d\n", n); err != nil {
				return
			}
			for i := 0; i < n; i++ {
				s, err := readLine()
				if err != nil {
					return
				}
				// f <frame> <stage> <raceCompletion> <lap> <hex>
				f := strings.Fields(s)
				if len(f) < 6 || f[0] != "f" {
					continue
				}
				frame, _ := strconv.Atoi(f[1])
				stage, _ := strconv.Atoi(f[2])
				rec, err := hex.DecodeString(f[5])
				if err != nil || len(rec) != records.RaceDataLen {
					continue
				}
				d.mu.Lock()
				d.frame = frame + 1
				d.stage = stage
				d.record = rec
				if stage >= 3 && !d.finished { // FinishLocal
					d.finished = true
					d.finishAt = frame - d.raceStart
				}
				d.mu.Unlock()
			}
		}
		fmt.Fprintln(stdin, "quit")
	}()
	return d, nil
}

// advance asks the driver to simulate up to frame (never blocks).
func (d *aiDriver) advance(frame int) {
	select {
	case d.target <- frame:
	default:
		// The driver is busy; it picks up the next target on a later tick.
	}
}

// latest returns a copy of the latest record with the bot's rank set, or nil.
func (d *aiDriver) latest(rank int) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.record == nil {
		return nil
	}
	r := append([]byte{}, d.record...)
	if d.rankBit > 0 && rank > 0 {
		records.PutBits(r, d.rankBit, 4, uint32(rank))
	}
	return r
}

func (d *aiDriver) stop() {
	close(d.target)
}

// aiBotState is a bot's AI driver for the current race.
type aiBotState struct {
	driver    *aiDriver
	course    common.Course
	raceStart time.Time // The reference's countdown start this driver races in.
	character byte
	vehicle   byte
	failed    bool // Couldn't start for this race (no line etc.): don't retry.
}

func newAIBotCombo(aid byte) (character, vehicle byte) {
	r := rand.New(rand.NewSource(int64(aid) * 7919))
	if r.Intn(10) == 0 {
		return aiDaisy, aiMachBike
	}
	return aiFunkyKong, aiFlameRunner
}

// aiBotRaceData returns the bot's AI RACEDATA record for this packet, starting or stepping
// its driver as needed. nil: no AI record (before the race, or no line), so the caller
// falls back. Caller must hold mu.
func aiBotRaceData(bot, ref *Aid, now time.Time) []byte {
	dir := room.config.AIBotDir
	if dir == "" || bot.bot == nil || ref == nil || ref.mirror == nil {
		return nil
	}
	ri := ref.mirror.records[common.RaceInfo]
	if ri == nil {
		return nil
	}
	info, err := records.ParseRaceInfo(ri)
	if err != nil || info.CountdownElapsedTime() == 0 {
		// Before the countdown: stop any driver from the last race.
		if s := bot.bot.ai; s != nil && s.driver != nil {
			s.driver.stop()
			s.driver = nil
		}
		if s := bot.bot.ai; s != nil {
			s.failed = false
		}
		return nil
	}
	course := info.Course()
	s := bot.bot.ai
	if s == nil {
		s = &aiBotState{}
		s.character, s.vehicle = newAIBotCombo(bot.aid)
		bot.bot.ai = s
	}
	if s.driver == nil && !s.failed {
		slot, players := aiGridSlot(ri, bot.aid)
		d, err := startAIDriver(dir, course, s.character, s.vehicle, slot, players)
		if err != nil {
			logging.Log("AI bot aid %d: %v", bot.aid, err)
			s.failed = true
			return nil
		}
		logging.Log("AI bot aid %d racing course %d from grid slot %d of %d", bot.aid, course, slot, players)
		s.driver, s.course = d, course
	}
	if s.driver == nil {
		return nil
	}
	// The reference's countdown frame now, extrapolated from their last packet.
	elapsed := int(info.CountdownElapsedTime()) + int(now.Sub(ref.mirror.lastAt).Seconds()*60)
	s.driver.advance(s.driver.countdown + elapsed)
	return s.driver.latest(0)
}

// aiGridSlot is the bot's 1-based start slot and the racer count, from the player id map in
// RaceInfo (player id order is the grid order).
func aiGridSlot(raceInfo []byte, aid byte) (slot, players int) {
	id := playerIdOf(raceInfo, aid)
	players = 12
	if n := raceInfoPlayerCount(raceInfo); n > 0 {
		players = n
	}
	if id < 0 {
		id = int(aid)
	}
	slot = id + 1
	if slot > players {
		players = slot
	}
	return slot, players
}

// aiBotFinish reports the AI bot's finishing time once its race is over.
func aiBotFinish(bot *Aid) (records.FinishTime, bool) {
	if bot.bot == nil || bot.bot.ai == nil || bot.bot.ai.driver == nil {
		return records.FinishTime{}, false
	}
	d := bot.bot.ai.driver
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.finished {
		return records.FinishTime{}, false
	}
	ms := d.finishAt * 1000 / 60
	return addMilliseconds(records.FinishTime{}, ms), true
}
