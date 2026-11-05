package core

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
)

// posRec builds a RaceData record at pos.
func posRec(t *testing.T, pos [3]float32) *records.RaceData {
	t.Helper()
	d := raceData(1)
	var v [3]uint32
	for i, f := range pos {
		v[i] = (math.Float32bits(f+1e6) - 0x3f800000) & 0x0fffffff
	}
	// x: top 28 bits at 0x0; y: low 28 bits at 0x3; z: top 28 bits at 0x7.
	binary.BigEndian.PutUint32(d[0x0:], v[0]<<4|uint32(d[3]&0x0f))
	y := binary.BigEndian.Uint32(d[0x3:])&0xf0000000 | v[1]
	binary.BigEndian.PutUint32(d[0x3:], y)
	binary.BigEndian.PutUint32(d[0x7:], v[2]<<4|uint32(d[0xa]&0x0f))
	rd, err := records.ParseRaceData(d)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}

func TestRecording(t *testing.T) {
	dir := t.TempDir()
	setupRoom(t, Config{RecordDir: dir})
	a := addTestAid(t, 0, false)
	now := time.Now()
	startRecording(common.LuigiCircuit, now)
	p := a.players[0]
	recordPos(a, 0, p, posRec(t, [3]float32{100, 200, 300}), now.Add(time.Second))
	recordPos(a, 0, p, posRec(t, [3]float32{110, 200, 300}), now.Add(time.Second+10*time.Millisecond)) // Rate limited.
	recordItem(a, 0, "granted", common.Banana, now.Add(2*time.Second))
	recordFlag(a, "test flag", now.Add(3*time.Second))
	stopRecording(now.Add(4 * time.Second))

	files, _ := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("%d recordings", len(files))
	}
	f, _ := os.Open(files[0])
	defer f.Close()
	var types []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var v map[string]any
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		types = append(types, v["type"].(string))
	}
	want := []string{"race", "player", "pos", "item", "flag", "end"}
	if len(types) != len(want) {
		t.Fatalf("lines %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("lines %v, want %v", types, want)
		}
	}
}
