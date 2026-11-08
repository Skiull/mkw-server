package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mkw-server/common"
	"mkw-server/logging"
)

// Saved runs per grid slot (lines/slot/): the fastest race an AI bot has driven from each
// course + grid slot + racer count, in the line format, with its time. Kinoko is
// deterministic, so a bot starting from the same slot replays a saved run frame for frame;
// every race is recorded, and a faster one replaces it. kinoko/out/improve_slots.py keeps
// racing saved slots with a bigger look-ahead budget to improve them.

type aiSlotMeta struct {
	Frames int    `json:"frames"` // From race start to the finish.
	Base   string `json:"base"`   // The ghost line it was driven from (combo, course).
	Date   string `json:"date"`
}

func aiSlotPath(dir string, course common.Course, slot, players int) string {
	return filepath.Join(dir, "lines", "slot", fmt.Sprintf("%02d-p%02d-n%02d", course, slot, players))
}

// aiSlotBest is the saved run for this slot, if it was driven from the same ghost line.
func aiSlotBest(dir string, course common.Course, slot, players int, base string) (string, bool) {
	p := aiSlotPath(dir, course, slot, players)
	meta, ok := readSlotMeta(p + ".json")
	if !ok || meta.Base != filepath.Base(base) {
		return "", false
	}
	if _, err := os.Stat(p + ".line"); err != nil {
		return "", false
	}
	return p + ".line", true
}

func aiSlotCandidate(dir string, course common.Course, slot, players int) string {
	os.MkdirAll(filepath.Join(dir, "lines", "slot"), 0o755)
	return fmt.Sprintf("%s.cand-%d.line", aiSlotPath(dir, course, slot, players), time.Now().UnixNano())
}

var aiSlotMu = &struct{ m chanMutex }{m: make(chanMutex, 1)}

type chanMutex chan struct{}

func (c chanMutex) lock()   { c <- struct{}{} }
func (c chanMutex) unlock() { <-c }

// aiSlotFinish keeps the candidate recording if the bot finished faster than the saved run.
func aiSlotFinish(dir string, course common.Course, slot, players int, base, candidate string, finished bool, frames int) {
	defer os.Remove(candidate)
	if !finished || frames <= 0 {
		return
	}
	aiSlotMu.m.lock()
	defer aiSlotMu.m.unlock()
	p := aiSlotPath(dir, course, slot, players)
	if meta, ok := readSlotMeta(p + ".json"); ok && meta.Base == filepath.Base(base) && meta.Frames <= frames {
		return
	}
	data, err := os.ReadFile(candidate)
	if err != nil || len(data) == 0 {
		return
	}
	if err := os.WriteFile(p+".line", data, 0o644); err != nil {
		return
	}
	// The ghost's .rkg alongside, for the combo.
	if rkg, err := os.ReadFile(base[:len(base)-len(".line")] + ".rkg"); err == nil {
		os.WriteFile(p+".rkg", rkg, 0o644)
	}
	meta, _ := json.Marshal(aiSlotMeta{Frames: frames, Base: filepath.Base(base), Date: time.Now().Format(time.RFC3339)})
	os.WriteFile(p+".json", meta, 0o644)
	logging.Log("AI bots: new best for course %d slot %d/%d: %.3fs", course, slot, players, float64(frames)/60)
}

func readSlotMeta(path string) (aiSlotMeta, bool) {
	var m aiSlotMeta
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &m) != nil {
		return m, false
	}
	return m, true
}
