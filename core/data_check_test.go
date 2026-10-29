package core

import (
	"testing"
	"time"
)

func TestDataChanges(t *testing.T) {
	setupRoom(t, Config{MutableDataBlocks: map[int]bool{3: true}, KickDataCheats: true})
	a := addTestAid(t, 0, false)
	// 10 blocks: block 3 changed (normal).
	handleDataChanges(a, []byte{'G', 'D', 0, 10, 0x10, 0x00}, time.Now())
	if a.kicked {
		t.Fatal("kicked for a normal change")
	}
	if got, ok := parseDataChanges([]byte{'G', 'D', 0, 10, 0x10, 0x40}); !ok || len(got) != 2 || got[0] != 3 || got[1] != 9 {
		t.Fatalf("parsed %v %v", got, ok)
	}
	// Block 9 changed too: not on the list.
	handleDataChanges(a, []byte{'G', 'D', 0, 10, 0x10, 0x40}, time.Now())
	if !a.kicked {
		t.Fatal("not kicked for an unexpected change")
	}
}
