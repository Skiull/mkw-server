package core

import (
	"encoding/binary"
	"testing"
	"time"
)

func hashPacket(h uint64) []byte {
	p := []byte{'G', 'H', 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint64(p[2:], h)
	return p
}

func TestGameHashKick(t *testing.T) {
	config := DefaultConfig()
	config.GameHashes = map[uint64]bool{0x1111: true}
	config.KickHashCheats = true
	setupRoom(t, config)
	a := addTestAid(t, 0, false)
	now := time.Now()

	mu.Lock()
	defer mu.Unlock()
	handleGameHash(a, hashPacket(0x1111), now)
	// One odd pass (e.g. while logging in) is forgiven.
	handleGameHash(a, hashPacket(0x2222), now)
	handleGameHash(a, hashPacket(0x1111), now)
	if a.kicked {
		t.Fatal("kicked for a single unknown pass")
	}
	handleGameHash(a, hashPacket(0x2222), now)
	handleGameHash(a, hashPacket(0x2222), now)
	if !a.kicked {
		t.Fatal("not kicked after two unknown passes in a row")
	}
}

func TestGameHashLogOnly(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a := addTestAid(t, 0, false)
	mu.Lock()
	defer mu.Unlock()
	for range 5 {
		handleGameHash(a, hashPacket(0x2222), time.Now())
	}
	if a.kicked {
		t.Fatal("kicked with no allowlist set")
	}
}
