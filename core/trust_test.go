package core

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTrustedPlayers(t *testing.T) {
	got, err := ParseTrustedPlayers("3699-5411-5651, 612345678")
	if err != nil {
		t.Fatal(err)
	}
	// 369954115651 & 0xffffffff
	if !got[uint32(369954115651&0xffffffff)] || !got[612345678] || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestTrustedPlayerLearning(t *testing.T) {
	file := filepath.Join(t.TempDir(), "learned.json")
	setupRoom(t, Config{TrustedPlayers: map[uint32]bool{42: true}, LearnFile: file, KickDataCheats: true})
	learnedLoginHashes = map[uint64]bool{}
	currentBuild = 0
	trusted := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	trusted.profileID, other.profileID = 42, 7

	gh := make([]byte, 10)
	gh[0], gh[1] = 'G', 'H'
	binary.BigEndian.PutUint64(gh[2:], 0xabc)
	handleGameHash(trusted, gh, time.Now())
	if room.config.GameHashes[0xabc] {
		t.Fatal("learned a game hash before knowing the build")
	}
	handleLoginHash(trusted, loginPacket(0xdef, 100)) // Learns the hash sent just before.
	if !room.config.GameHashes[0xabc] || currentBuild != 100 {
		t.Fatal("trusted game hash not learned with the login packet")
	}
	room.config.GameHashes = map[uint64]bool{}
	handleGameHash(other, gh, time.Now())
	if room.config.GameHashes[0xabc] {
		t.Fatal("learned a game hash from an untrusted player")
	}
	handleGameHash(trusted, gh, time.Now())
	if !room.config.GameHashes[0xabc] {
		t.Fatal("trusted game hash not learned")
	}


	// Trusted player changes block 9: learned, not kicked. Then someone else changing it
	// is normal too.
	handleDataChanges(trusted, []byte{'G', 'D', 0, 10, 0x00, 0x40}, time.Now())
	if trusted.kicked || !room.config.MutableDataBlocks[9] {
		t.Fatal("trusted data block not learned")
	}
	handleDataChanges(other, []byte{'G', 'D', 0, 10, 0x00, 0x40}, time.Now())
	if other.kicked {
		t.Fatal("kicked for a block a trusted player changed")
	}

	// Saved for wfc-server (written in the background).
	var f learnedFile
	for range 50 {
		if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &f) == nil && len(f.LoginHashes) > 0 && len(f.MutableDataBlocks) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(f.LoginHashes) != 1 || f.LoginHashes[0] != "0000000000000def" {
		t.Fatalf("saved login hashes %v", f.LoginHashes)
	}
}

func loginPacket(hash uint64, build uint32) []byte {
	gl := make([]byte, 14)
	gl[0], gl[1] = 'G', 'L'
	binary.BigEndian.PutUint64(gl[2:], hash)
	binary.BigEndian.PutUint32(gl[10:], build)
	return gl
}

func TestNewBuildReplacesOld(t *testing.T) {
	file := filepath.Join(t.TempDir(), "learned.json")
	setupRoom(t, Config{TrustedPlayers: map[uint32]bool{42: true}, LearnFile: file})
	learnedLoginHashes = map[uint64]bool{1: true}
	challengeAnswers = map[uint64]map[uint32]uint64{0xabc: {5: 6}}
	room.config.GameHashes = map[uint64]bool{0xabc: true}
	currentBuild = 100
	trusted := addTestAid(t, 0, false)
	trusted.profileID = 42

	// A trusted player on an older build teaches nothing and changes nothing.
	handleLoginHash(trusted, loginPacket(0x111, 50))
	if currentBuild != 100 || learnedLoginHashes[0x111] {
		t.Fatal("learned from an older build")
	}

	// A newer build forgets everything from the old one.
	trusted.gameHash = 0x222
	handleLoginHash(trusted, loginPacket(0x333, 200))
	if currentBuild != 200 || room.config.GameHashes[0xabc] || learnedLoginHashes[1] || len(challengeAnswers) != 0 {
		t.Fatal("old build still accepted")
	}
	if !room.config.GameHashes[0x222] || !learnedLoginHashes[0x333] {
		t.Fatal("new build not learned")
	}

	var f learnedFile
	for range 50 {
		if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &f) == nil && len(f.LoginHashes) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if f.Build != 200 || len(f.GameHashes) != 1 || f.GameHashes[0] != "0000000000000222" || len(f.LoginHashes) != 1 {
		t.Fatalf("saved %+v", f)
	}
}
