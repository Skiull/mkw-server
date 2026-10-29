package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mkw-server/common"
)

func TestRecording(t *testing.T) {
	dir := t.TempDir()
	setupRoom(t, Config{RecordDir: dir})
	a := addTestAid(t, 0, false)
	now := time.Now()
	startRecording(common.LuigiCircuit, now)
	p := a.players[0]
	recordPos(a, 0, p, moveRec(t, [3]float32{100, 200, 300}, nil), now.Add(time.Second))
	recordPos(a, 0, p, moveRec(t, [3]float32{110, 200, 300}, nil), now.Add(time.Second+10*time.Millisecond)) // Rate limited.
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
