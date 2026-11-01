package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
)

func TestPacketRecordingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	setupRoom(t, Config{RecordDir: dir, RecordPackets: true})
	a := addTestAid(t, 3, false)
	a.raceStarted = true
	a.mirror = &mirrorSource{}
	now := time.Now()
	startRecording(common.LuigiCircuit, now)

	rd := func(b byte) []byte { return bytes.Repeat([]byte{b}, records.RaceDataLen) }
	a.mirror.records[common.RaceData] = rd(1)
	recordPackets(a, now.Add(100*time.Millisecond))
	recordPackets(a, now.Add(133*time.Millisecond)) // Unchanged: not written.
	a.mirror.records[common.RaceData] = rd(2)
	recordPackets(a, now.Add(1000*time.Millisecond))
	stopRecording(now.Add(2 * time.Second))

	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.pkt"))
	if len(files) != 1 {
		t.Fatalf("%d packet recordings", len(files))
	}
	if _, err := os.Stat(files[0][:len(files[0])-4] + ".players.json"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replay = nil })
	if err := LoadReplay(files[0]); err != nil {
		t.Fatal(err)
	}
	if replay.course != common.LuigiCircuit || len(replay.players) != 1 || replay.players[0].aid != 3 {
		t.Fatalf("replay %+v", replay)
	}
	p := replay.players[0]
	if len(p.raceData) != 2 {
		t.Fatalf("%d RaceData frames", len(p.raceData))
	}
	for ms, want := range map[int64]byte{0: 1, 500: 1, 1000: 2, 5000: 2} {
		if got := p.replayRaceData(ms); got[0] != want {
			t.Errorf("at %dms got frame %d, want %d", ms, got[0], want)
		}
	}
}

func TestReplayRoomDoesNotRecord(t *testing.T) {
	dir := t.TempDir()
	setupRoom(t, Config{RecordDir: dir, RecordPackets: true, ReplayFile: "x.pkt"})
	startRecording(common.LuigiCircuit, time.Now())
	if rec != nil || pktRec != nil {
		t.Fatal("replay room recorded a race")
	}
}
