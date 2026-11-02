package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

// A 12-player race leaves no room for all 12 bots next to the watcher: the watcher's own
// recorded player becomes the first bot's guest.
func TestReplaySeatsWatcherAsGuest(t *testing.T) {
	setupRoom(t, Config{ReplaySpectate: true})
	t.Cleanup(func() { replay = nil })
	replay = &replayData{}
	for i := 0; i < 12; i++ {
		replay.players = append(replay.players, &replayPlayer{aid: byte(i), profile: uint32(100 + i)})
	}
	for i := 0; i < 11; i++ {
		b := addTestAid(t, byte(i), false)
		b.bot = newBotState(byte(i))
	}
	w := addTestAid(t, 11, false)
	w.profileID = 105 // The watcher raced as recorded aid 5.

	seats := replaySeats()
	if len(seats) != 11 {
		t.Fatalf("%d seats", len(seats))
	}
	if g := seats[0].guest; g == nil || g.aid != 5 {
		t.Fatalf("guest %+v, want recorded aid 5", g)
	}
	seen := map[byte]bool{}
	for _, s := range seats {
		seen[s.main.aid] = true
		if s.guest != nil {
			seen[s.guest.aid] = true
		}
	}
	if len(seen) != 12 {
		t.Fatalf("%d recorded players shown, want 12", len(seen))
	}
	if m := replayAidMap(seats); m[5] != 0 || m[0] != 0 {
		t.Fatalf("aid map %v", m)
	}
}

func TestPruneKeepsNewestRecordings(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "2026-11-01")
	os.MkdirAll(day, 0o755)
	for i := 0; i < 15; i++ {
		base := filepath.Join(day, fmt.Sprintf("%06d_course8_1", 100000+i))
		os.WriteFile(base+".pkt", nil, 0o644)
		os.WriteFile(base+".players.json", nil, 0o644)
		os.WriteFile(base+".jsonl", nil, 0o644)
	}
	prunePacketRecordings(dir, 12)
	pkts, _ := filepath.Glob(filepath.Join(day, "*.pkt"))
	lists, _ := filepath.Glob(filepath.Join(day, "*.players.json"))
	logs, _ := filepath.Glob(filepath.Join(day, "*.jsonl"))
	if len(pkts) != 12 || len(lists) != 12 || len(logs) != 15 || filepath.Base(pkts[0]) != "100003_course8_1.pkt" {
		t.Fatalf("%d pkt, %d lists, %d jsonl, oldest %s", len(pkts), len(lists), len(logs), pkts[0])
	}
}

func TestRecordingKeepsUserAndProfile(t *testing.T) {
	dir := t.TempDir()
	setupRoom(t, Config{RecordDir: dir, RecordPackets: true})
	a := addTestAid(t, 2, false)
	a.profileID = 612345678
	a.raceStarted = true
	user := bytes.Repeat([]byte{7}, records.UserLen)
	a.mirror = &mirrorSource{lastUser: user} // Sent in the lobby, not in this packet.
	now := time.Now()
	startRecording(common.LuigiCircuit, now)
	a.mirror.records[common.RaceData] = bytes.Repeat([]byte{1}, records.RaceDataLen)
	recordPackets(a, now.Add(time.Second))
	stopRecording(now.Add(2 * time.Second))

	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.pkt"))
	t.Cleanup(func() { replay = nil })
	if err := LoadReplay(files[0]); err != nil {
		t.Fatal(err)
	}
	p := replay.players[0]
	if p.profile != 612345678 || len(p.frames[common.User]) != 1 || !bytes.Equal(p.frames[common.User][0].data, user) {
		t.Fatalf("profile %d, %d user frames", p.profile, len(p.frames[common.User]))
	}
}

func TestReplayEndsAfterLastFinish(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(replayMagic)
	frame := func(ms uint32, aid byte, t common.RecordIdx, data []byte) {
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[0:], ms)
		hdr[4], hdr[5] = aid, byte(t)
		binary.BigEndian.PutUint16(hdr[6:], uint16(len(data)))
		buf.Write(hdr[:])
		buf.Write(data)
	}
	rm := make([]byte, 0x26)
	frame(1000, 0, common.RaceMode, rm)
	finished := append([]byte{}, rm...)
	records.PutBits(finished, 45, 22, 1<<16) // 1:00.000
	frame(90000, 0, common.RaceMode, finished)
	frame(150000, 0, common.RaceMode, rm) // The next lobby.
	r, err := parseReplay(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if r.lengthMs != 90000+replayAfterFinish.Milliseconds() {
		t.Fatalf("length %d", r.lengthMs)
	}
}
