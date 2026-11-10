package core

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/logging"
)

// Human lines: AI bots learn from how players drive. The race recordings (.pkt) hold every
// player's RACEDATA for every frame, which carries nearly everything a line frame does
// (position, rotation, speed, stick, buttons, tricks). The course is cut into sections along
// the bot's line; wherever a player drove a section faster than the bot does, and cleanly (no
// hits, respawns or item boosts), that stretch of their run is spliced into the bot's line.
// Each splice is kept only if a bot racing the new line in Kinoko (real game physics, so a
// cheated run can't help) actually finishes faster. The result is lines/hl-<base line>, which
// bots drive when their grid spot has no saved run yet; saved runs are re-raced from it and
// replaced when it's faster.

// lineFrame is one frame of a Kinoko .line file (KLineSystem::Frame, little endian).
type lineFrame struct {
	Frame          uint32
	Pos            [3]float32
	Rot            [4]float32 // x, y, z, w
	Speed          float32
	RaceCompletion float32
	Checkpoint     uint16
	Buttons        uint16
	StickX, StickY float32
	Trick          uint8
	Pad            [3]uint8
}

func readLine(path string) ([]lineFrame, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]lineFrame, len(data)/56)
	if err := binary.Read(bytes.NewReader(data[:len(out)*56]), binary.LittleEndian, out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeLine(path string, line []lineFrame) error {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, line)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RACEDATA fields as Kinoko's encoder writes them (host/KRaceData.cc), by stream bit.
const (
	rdSpeedBit = 135 // 19 bits, m=15, k=1001
	rdRotBit   = 192 // 4 x 25 bits, m=23, k=2
	rdFlagsBit = 308 // 16 bits, flag k at bit 308+15-k
	rdTrickBit = 388 // 3 bits: the last trick's direction
	// Flips with each Mushroom used (word A bit 26).
	rdMushroomBit = 355 - 26
)

func decodeRDFloat(raw uint32, m uint, k float32) float32 {
	return math.Float32frombits((raw<<(23-m))+0x3f800000) - k
}

func rdFlag(data []byte, k int) bool {
	return records.GetBits(data, rdFlagsBit+15-k, 1) != 0
}

// humanSample is one frame of a player's run.
type humanSample struct {
	frame float64 // Game frame (countdown frame count).
	pos   [3]float32
	rot   [4]float32
	speed float32
	stick [2]float32
	btn   uint16 // Line button bits.
	trick uint8
	dirty bool // Something a bot can't do happened since the last sample.
}

type humanRun struct {
	src     string
	aid     byte
	vehicle common.Vehicle
	samples []humanSample
}

// humanRunOf turns a recorded player's RACEDATA into samples, by the frame counter of their
// RaceInfo. Returns nil without usable data.
func humanRunOf(src string, p *replayPlayer) *humanRun {
	ri := p.frames[common.RaceInfo]
	if len(ri) == 0 || len(p.raceData) == 0 {
		return nil
	}
	run := &humanRun{src: src, aid: p.aid, vehicle: p.vehicle}
	var prev *records.RaceData
	j := 0
	lastFrame, dirtyUntil := -1.0, 0.0
	for _, fr := range p.raceData {
		for j+1 < len(ri) && ri[j+1].ms <= fr.ms {
			j++
		}
		info, err := records.ParseRaceInfo(ri[j].data)
		if err != nil || info.CountdownElapsedTime() == 0 {
			prev = nil
			continue
		}
		frame := float64(info.CountdownElapsedTime())
		// RaceInfo arrives once per packet like RaceData; a duplicate frame number means the
		// RaceInfo for this one wasn't recorded yet: count on from the last one.
		if frame <= lastFrame {
			frame = lastFrame + 1
		}
		rd, err := records.ParseRaceData(fr.data)
		if err != nil || !rd.HasPosition() {
			prev = nil
			continue
		}
		d := fr.data
		s := humanSample{frame: frame, pos: rd.WorldPos()}
		for i := range s.pos {
			s.pos[i]-- // Kinoko's k is 1000001.
		}
		s.speed = decodeRDFloat(records.GetBits(d, rdSpeedBit, 19), 15, 1001)
		for i := 0; i < 4; i++ {
			s.rot[i] = decodeRDFloat(records.GetBits(d, rdRotBit+25*i, 25), 23, 2)
		}
		s.stick = [2]float32{(float32(rd.StickX) - 7) / 7, (float32(rd.StickY) - 7) / 7}
		if rdFlag(d, 0) {
			s.btn |= 0x1
		}
		if rdFlag(d, 1) {
			s.btn |= 0x2
		}
		if rdFlag(d, 2) { // Drift input
			s.btn |= 0x8
		}
		s.dirty = rd.Respawning() || rdFlag(d, 4) || rdFlag(d, 10) || rdFlag(d, 11)
		if prev != nil {
			pd := prev.Raw()
			if rd.TrickFlip() != prev.TrickFlip() {
				s.trick = uint8(records.GetBits(d, rdTrickBit, 3))
			}
			// Items bots don't have: dirty for as long as their effect lasts.
			if records.GetBits(d, rdMushroomBit, 1) != records.GetBits(pd, rdMushroomBit, 1) {
				dirtyUntil = max(dirtyUntil, frame+120)
			}
			if rd.StarFlip() != prev.StarFlip() || rd.MegaFlip() != prev.MegaFlip() ||
				rd.BulletBillFlip() != prev.BulletBillFlip() || rd.CloudZapFlip() != prev.CloudZapFlip() {
				dirtyUntil = max(dirtyUntil, frame+620)
			}
			if rd.KartActions() != prev.KartActions() || rd.HitFlags() != prev.HitFlags() ||
				rd.ThwompFlip() != prev.ThwompFlip() {
				s.dirty = true
			}
		}
		if frame < dirtyUntil {
			s.dirty = true
		}
		// A gap in the recording: unknown what happened in it.
		if lastFrame >= 0 && frame-lastFrame > 6 {
			s.dirty = true
		}
		prev, lastFrame = rd, frame
		run.samples = append(run.samples, s)
	}
	if len(run.samples) < 600 {
		return nil
	}
	return run
}

// A section boundary: a plane across the line at one of its points.
type hlBoundary struct {
	idx int // Base line frame.
	p   [3]float32
	n   [3]float32 // Flat unit tangent of the line there.
}

const (
	hlSectionFrames = 90   // Line frames per section.
	hlCrossRadius   = 2000 // How near the line point a crossing must be.
	hlCrossHeight   = 500  // ... and how near in height (not a crossing on another level).
	hlBlendFrames   = 20   // Spliced runs are eased onto the line over this many frames.
	hlMinGain       = 4    // Frames a section must be faster by to try it.
	hlMaxCandidates = 24
)

func hlBoundaries(line []lineFrame) []hlBoundary {
	start, end := -1, len(line)
	for i, f := range line {
		if start < 0 && f.RaceCompletion >= 1 {
			start = i
		}
		if f.RaceCompletion >= 4 {
			end = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var out []hlBoundary
	for i := start + 60; i < end-10; i += hlSectionFrames {
		a, b := line[max(i-5, 0)].Pos, line[min(i+5, len(line)-1)].Pos
		d := sub3f(b, a)
		d[1] = 0
		l := float32(math.Hypot(float64(d[0]), float64(d[2])))
		if l < 1 {
			continue // Standing still (or straight up): no direction.
		}
		out = append(out, hlBoundary{idx: i, p: line[i].Pos, n: [3]float32{d[0] / l, 0, d[2] / l}})
	}
	return out
}

func sub3f(a, b [3]float32) [3]float32 { return [3]float32{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func dot3f(a, b [3]float32) float32  { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

// crossings finds when a path crosses each boundary, in order: cross[k] is the fractional
// sample index of crossing boundary k, or -1. A path that loses the line (fell off, took
// another route) picks it up again at any of the next few boundaries.
func crossings(pos func(i int) [3]float32, n int, bs []hlBoundary) []float64 {
	cross := make([]float64, len(bs))
	for k := range cross {
		cross[k] = -1
	}
	next := 0
	for i := 0; i+1 < n && next < len(bs); i++ {
		a, b := pos(i), pos(i+1)
		for k := next; k < min(next+6, len(bs)); k++ {
			bd := bs[k]
			da, db := dot3f(sub3f(a, bd.p), bd.n), dot3f(sub3f(b, bd.p), bd.n)
			if !(da < 0 && db >= 0) {
				continue
			}
			off := sub3f(b, bd.p)
			if abs32(off[1]) > hlCrossHeight || math.Hypot(float64(off[0]), float64(off[2])) > hlCrossRadius {
				continue
			}
			cross[k] = float64(i) + float64(da/(da-db))
			next = k + 1
			break
		}
	}
	return cross
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// hlCandidate is a stretch of a player's run to splice over base line frames [from, to).
type hlCandidate struct {
	from, to   int // Base line frames.
	run        *humanRun
	sFrom, sTo float64 // Sample indices.
	gain       float64 // Frames faster than the bot.
}

// sectionCandidates finds, per section, the fastest clean player run that beats the bot's
// time there (botTimes[k] is the bot's frames for section k, +Inf where it never got there),
// and merges neighbouring sections from the same run into one stretch.
func sectionCandidates(runs []*humanRun, bs []hlBoundary, botTimes []float64) []hlCandidate {
	type best struct {
		frames float64
		run    *humanRun
		a, b   float64
	}
	bests := make([]best, len(bs)-1)
	for k := range bests {
		bests[k].frames = math.Inf(1)
	}
	for _, r := range runs {
		cross := crossings(func(i int) [3]float32 { return r.samples[i].pos }, len(r.samples), bs)
		for k := 0; k+1 < len(bs); k++ {
			a, b := cross[k], cross[k+1]
			if a < 0 || b < 0 {
				continue
			}
			clean := true
			for i := int(a); i <= int(math.Ceil(b)) && i < len(r.samples); i++ {
				if r.samples[i].dirty {
					clean = false
					break
				}
			}
			if !clean {
				continue
			}
			frames := sampleFrame(r, b) - sampleFrame(r, a)
			if frames > 0 && frames < bests[k].frames {
				bests[k] = best{frames, r, a, b}
			}
		}
	}
	var out []hlCandidate
	for k, b := range bests {
		if b.run == nil || b.frames > botTimes[k]-hlMinGain {
			continue
		}
		gain := math.Min(botTimes[k]-b.frames, 600)
		if n := len(out); n > 0 && out[n-1].run == b.run && out[n-1].to == bs[k].idx && math.Abs(out[n-1].sTo-b.a) < 0.01 {
			out[n-1].to, out[n-1].sTo = bs[k+1].idx, b.b
			out[n-1].gain += gain
			continue
		}
		out = append(out, hlCandidate{from: bs[k].idx, to: bs[k+1].idx, run: b.run, sFrom: b.a, sTo: b.b, gain: gain})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].gain > out[j].gain })
	if len(out) > hlMaxCandidates {
		out = out[:hlMaxCandidates]
	}
	return out
}

func sampleFrame(r *humanRun, x float64) float64 {
	i := int(x)
	if i+1 >= len(r.samples) {
		return r.samples[len(r.samples)-1].frame
	}
	f := x - float64(i)
	return r.samples[i].frame*(1-f) + r.samples[i+1].frame*f
}

// sampleAt is the run's state at a game frame (positions interpolated, inputs held).
func sampleAt(r *humanRun, frame float64) humanSample {
	s := r.samples
	i := sort.Search(len(s), func(i int) bool { return s[i].frame > frame }) - 1
	if i < 0 {
		return s[0]
	}
	if i+1 >= len(s) {
		return s[len(s)-1]
	}
	out := s[i]
	if span := s[i+1].frame - s[i].frame; span > 0 {
		f := float32((frame - s[i].frame) / span)
		for k := 0; k < 3; k++ {
			out.pos[k] += (s[i+1].pos[k] - s[i].pos[k]) * f
		}
	}
	return out
}

// splice builds a line from base with the candidates' stretches in place of the frames they
// cover. Candidates must not overlap.
func splice(base []lineFrame, cands []hlCandidate) []lineFrame {
	cs := append([]hlCandidate{}, cands...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].from < cs[j].from })
	var out []lineFrame
	at := 0
	for _, c := range cs {
		out = append(out, base[at:c.from]...)
		f0, f1 := sampleFrame(c.run, c.sFrom), sampleFrame(c.run, c.sTo)
		n := int(math.Round(f1 - f0))
		if n < 2 {
			at = c.from
			continue
		}
		first, last := sampleAt(c.run, f0), sampleAt(c.run, f0+float64(n-1))
		d0, d1 := sub3f(base[c.from].Pos, first.pos), sub3f(base[c.to].Pos, last.pos)
		for u := 0; u < n; u++ {
			s := sampleAt(c.run, f0+float64(u))
			w0 := max(0, 1-float32(u)/hlBlendFrames)
			w1 := max(0, 1-float32(n-1-u)/hlBlendFrames)
			lf := lineFrame{Rot: s.rot, Speed: s.speed, Buttons: s.btn, StickX: s.stick[0], StickY: s.stick[1], Trick: s.trick}
			for k := 0; k < 3; k++ {
				lf.Pos[k] = s.pos[k] + d0[k]*w0 + d1[k]*w1
			}
			t := float32(u) / float32(n)
			lf.RaceCompletion = base[c.from].RaceCompletion + (base[c.to].RaceCompletion-base[c.from].RaceCompletion)*t
			lf.Checkpoint = base[c.from+int(float32(c.to-c.from)*t)].Checkpoint
			out = append(out, lf)
		}
		at = c.to
	}
	out = append(out, base[at:]...)
	for i := range out {
		out[i].Frame = uint32(i)
	}
	return out
}

func overlaps(a hlCandidate, cs []hlCandidate) bool {
	for _, c := range cs {
		if a.from < c.to && c.from < a.to {
			return true
		}
	}
	return false
}

var finishRe = regexp.MustCompile(`Bot finished: \d+ frames, time (\d+):(\d+)\.(\d+)`)

// kinokoRace races a line in Kinoko from a grid spot and returns its frames from the race
// start to the finish (0: didn't finish). record, if set, saves the run in the line format.
func kinokoRace(dir, line, rkg string, slot, players int, record string) int {
	maxFrames := 412 + 6*60*60
	if d, err := os.ReadFile(rkg); err == nil && len(d) > 7 {
		ms := (int(d[4]>>1)*60+int((d[4]&1)<<6|d[5]>>2))*1000 + (int(d[5]&3)<<8 | int(d[6]))
		maxFrames = 412 + int(float64(ms)*0.06*1.6) + 600
	}
	args := []string{"bot", "-g", rkg, "-l", line, "-p", strconv.Itoa(slot), "-n", strconv.Itoa(players),
		"-o", strconv.Itoa(aiObjectDelayFrames), "-M", strconv.Itoa(maxFrames)}
	if record != "" {
		args = append(args, "-w", record)
	}
	cmd := exec.Command(filepath.Join(dir, "kinoko.exe"), args...)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	m := finishRe.FindSubmatch(out)
	if m == nil {
		return 0
	}
	mi, _ := strconv.Atoi(string(m[1]))
	se, _ := strconv.Atoi(string(m[2]))
	ms, _ := strconv.Atoi(string(m[3]))
	return int(math.Round(float64((mi*60+se)*1000+ms) * 0.06))
}

func better(a, b int) bool { return a > 0 && (b == 0 || a < b) }

type hlMeta struct {
	Base       string   `json:"base"`
	Frames     int      `json:"frames"`     // The bot's time on this line (grid spot 1 of 1).
	BaseFrames int      `json:"baseFrames"` // ... and on the base line (0: didn't finish).
	Splices    []string `json:"splices"`
	Date       string   `json:"date"`
}

// humanLinePath is the human-improved version of a base line.
func humanLinePath(base string) string {
	return filepath.Join(filepath.Dir(base), "hl-"+filepath.Base(base))
}

// BuildHumanLines improves the AI bots' lines with the players' runs in recordDir's race
// recordings. course < 0 does every course with recordings. Slow: each try is a Kinoko race.
func BuildHumanLines(recordDir, aiDir string, course int) error {
	paths, _ := filepath.Glob(filepath.Join(recordDir, "*", "*.pkt"))
	byCourse := map[common.Course][]*humanRun{}
	for _, p := range paths {
		r, err := loadRecording(p)
		if err != nil {
			continue
		}
		if course >= 0 && int(r.course) != course {
			continue
		}
		for _, pl := range r.players {
			if run := humanRunOf(filepath.Base(p), pl); run != nil {
				byCourse[r.course] = append(byCourse[r.course], run)
			}
		}
	}
	if len(byCourse) == 0 {
		return fmt.Errorf("no player runs in %s", recordDir)
	}
	var courses []int
	for c := range byCourse {
		courses = append(courses, int(c))
	}
	sort.Ints(courses)
	for _, c := range courses {
		done := map[string]bool{}
		for _, v := range []byte{aiFlameRunner, aiMachBike} {
			base := aiLineFor(aiDir, common.Course(c), v)
			if base == "" || done[base] {
				continue
			}
			done[base] = true
			if err := buildHumanLine(aiDir, common.Course(c), base, byCourse[common.Course(c)]); err != nil {
				logging.Log("Human lines: course %d: %v", c, err)
			}
		}
	}
	return nil
}

// loadRecording reads a .pkt recording with its player list; players without a profile
// (bots) are left out.
func loadRecording(path string) (*replayData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	r, err := parseReplay(gz)
	if err != nil {
		return nil, err
	}
	meta, err := os.ReadFile(strings.TrimSuffix(path, ".pkt") + ".players.json")
	if err != nil {
		return nil, err
	}
	var m struct {
		Course   int
		Profiles map[string]uint32
	}
	if json.Unmarshal(meta, &m) != nil {
		return nil, fmt.Errorf("bad player list")
	}
	r.course = common.Course(m.Course)
	var real []*replayPlayer
	for _, p := range r.players {
		if m.Profiles[fmt.Sprint(p.aid)] != 0 {
			real = append(real, p)
		}
	}
	r.players = real
	return r, nil
}

func buildHumanLine(dir string, course common.Course, base string, runs []*humanRun) error {
	line, err := readLine(base)
	if err != nil {
		return err
	}
	rkg := strings.TrimSuffix(base, ".line") + ".rkg"
	_, lineVehicle, _ := aiGhostCombo(base)
	bike := lineVehicle >= byte(common.StandardBikeS)
	var use []*humanRun
	for _, r := range runs {
		if (byte(r.vehicle) >= byte(common.StandardBikeS)) == bike {
			use = append(use, r)
		}
	}
	if len(use) == 0 {
		return fmt.Errorf("no runs on a matching vehicle type")
	}
	bs := hlBoundaries(line)
	if len(bs) < 2 {
		return fmt.Errorf("line too short")
	}
	tmp, err := os.MkdirTemp("", "hl")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// How the bot actually drives the base line, section by section.
	rec := filepath.Join(tmp, "base-run.line")
	baseFrames := kinokoRace(dir, base, rkg, 1, 1, rec)
	botTimes := make([]float64, len(bs)-1)
	for k := range botTimes {
		botTimes[k] = math.Inf(1)
	}
	if run, err := readLine(rec); err == nil && len(run) > 0 {
		cross := crossings(func(i int) [3]float32 { return run[i].Pos }, len(run), bs)
		for k := range botTimes {
			if cross[k] >= 0 && cross[k+1] >= 0 {
				botTimes[k] = cross[k+1] - cross[k]
			}
		}
	}
	cands := sectionCandidates(use, bs, botTimes)
	logging.Log("Human lines: course %d %s: bot %s, %d runs, %d sections to try", course, filepath.Base(base), fmtFrames(baseFrames), len(use), len(cands))
	if len(cands) == 0 {
		return nil
	}

	// Each stretch alone, in parallel.
	single := make([]int, len(cands))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < max(1, runtime.NumCPU()/2); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p := filepath.Join(tmp, fmt.Sprintf("single-%d.line", i))
				if writeLine(p, splice(line, cands[i:i+1])) == nil {
					single[i] = kinokoRace(dir, p, rkg, 1, 1, "")
				}
			}
		}()
	}
	for i := range cands {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// Then together, best first, keeping each one that still helps.
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return better(single[order[a]], single[order[b]]) })
	var kept []hlCandidate
	best := baseFrames
	try := filepath.Join(tmp, "try.line")
	for _, i := range order {
		if !better(single[i], baseFrames) && baseFrames > 0 {
			continue
		}
		if overlaps(cands[i], kept) {
			continue
		}
		next := append(append([]hlCandidate{}, kept...), cands[i])
		if writeLine(try, splice(line, next)) != nil {
			continue
		}
		if f := kinokoRace(dir, try, rkg, 1, 1, ""); better(f, best) {
			kept, best = next, f
		}
	}
	if len(kept) == 0 {
		logging.Log("Human lines: course %d %s: no section helped", course, filepath.Base(base))
		return nil
	}
	out := humanLinePath(base)
	if err := writeLine(out, splice(line, kept)); err != nil {
		return err
	}
	copyFile(rkg, strings.TrimSuffix(out, ".line")+".rkg")
	meta := hlMeta{Base: filepath.Base(base), Frames: best, BaseFrames: baseFrames, Date: time.Now().Format(time.RFC3339)}
	for _, c := range kept {
		meta.Splices = append(meta.Splices, fmt.Sprintf("frames %d-%d from %s aid %d (%.0f frames faster alone)", c.from, c.to, c.run.src, c.run.aid, c.gain))
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(strings.TrimSuffix(out, ".line")+".json", data, 0o644)
	logging.Log("Human lines: course %d %s: %s -> %s with %d player sections", course, filepath.Base(base), fmtFrames(baseFrames), fmtFrames(best), len(kept))

	reraceSlots(dir, course, base, out)
	return nil
}

// reraceSlots races the human line from each grid spot of the course that has a saved run,
// and replaces the run where it's faster (a saved run otherwise stays: bots replay it).
func reraceSlots(dir string, course common.Course, base, hl string) {
	metas, _ := filepath.Glob(filepath.Join(dir, "lines", "slot", fmt.Sprintf("%02d-*.json", course)))
	rkg := strings.TrimSuffix(base, ".line") + ".rkg"
	for _, mp := range metas {
		meta, ok := readSlotMeta(mp)
		if !ok || meta.Base != filepath.Base(base) {
			continue
		}
		p := strings.TrimSuffix(mp, ".json")
		cand := p + fmt.Sprintf(".cand-hl-%d.line", time.Now().UnixNano())
		f := kinokoRace(dir, hl, rkg, meta.Slot, meta.Players, cand)
		if better(f, meta.Frames) {
			aiSlotMu.m.lock()
			os.Rename(cand, p+".line")
			old := meta.Frames
			meta.Frames, meta.Date = f, time.Now().Format(time.RFC3339)
			data, _ := json.Marshal(meta)
			os.WriteFile(mp, data, 0o644)
			aiSlotMu.m.unlock()
			logging.Log("Human lines: course %d spot %s: %s -> %s", course, filepath.Base(p), fmtFrames(old), fmtFrames(f))
		}
		os.Remove(cand)
	}
}

func fmtFrames(f int) string {
	if f <= 0 {
		return "DNF"
	}
	return fmt.Sprintf("%.3fs", float64(f)/60)
}

func copyFile(from, to string) {
	if data, err := os.ReadFile(from); err == nil {
		os.WriteFile(to, data, 0o644)
	}
}
