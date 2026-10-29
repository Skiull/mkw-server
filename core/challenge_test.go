package core

import (
	"encoding/binary"
	"path/filepath"
	"testing"
	"time"
)

func reply(seed uint32, answer uint64) []byte {
	d := make([]byte, 14)
	d[0], d[1] = 'H', 'R'
	binary.BigEndian.PutUint32(d[2:], seed)
	binary.BigEndian.PutUint64(d[6:], answer)
	return d
}

func TestChallengeLearnAndCheck(t *testing.T) {
	file := filepath.Join(t.TempDir(), "challenges.json")
	challengeAnswers = map[uint64]map[uint32]uint64{}
	setupRoom(t, Config{ChallengeFile: file, LearnChallenges: true})
	a := addTestAid(t, 0, false)
	a.gameHash = 0xabc
	now := time.Now()

	// Learning: each due step asks the next seed; answers are stored.
	asked := map[uint32]bool{}
	for range 3 {
		stepChallenge(a, now)
		seed := a.challenge.seed
		if !a.challenge.pending || asked[seed] {
			t.Fatalf("learning asked %d again (pending %v)", seed, a.challenge.pending)
		}
		asked[seed] = true
		handleChallengeReply(a, reply(seed, uint64(seed)*7))
		now = now.Add(time.Second)
	}
	if len(challengeAnswers[0xabc]) != 3 {
		t.Fatalf("learned %d answers", len(challengeAnswers[0xabc]))
	}

	// Checking: a wrong answer kicks.
	room.config.LearnChallenges, room.config.KickChallengeFails = false, true
	a.challenge = challengeState{}
	stepChallenge(a, now)
	seed := a.challenge.seed
	handleChallengeReply(a, reply(seed, uint64(seed)*7))
	if a.kicked {
		t.Fatal("kicked for a right answer")
	}
	a.challenge.nextAt = time.Time{}
	stepChallenge(a, now.Add(time.Minute))
	handleChallengeReply(a, reply(a.challenge.seed, 12345))
	if !a.kicked {
		t.Fatal("not kicked for a wrong answer")
	}
}
