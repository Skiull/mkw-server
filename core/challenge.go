package core

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"mkw-server/logging"
)

// Game hash challenges. A client reports its own game hash, so a modified client could
// send a known-good one. To check, the server sends 'H' 'C' + seed (u32); the client hashes
// 64 pseudo-random pieces of its code picked by the seed and replies 'H' 'R' + seed + hash
// (u64). The server can't compute the answer itself (it has no copy of the game's memory),
// so it learns the answers for each known-good game hash from a clean client first
// (-learn-challenges): 1024 random seeds out of 4 billion, kept only in the server's file.
// A cheater can't know which will be asked, so they can't ship a table of answers; they'd
// need a clean copy of the game in memory to hash. A wrong answer, or none, means the
// client isn't what its game hash claims.

const (
	challengeSeeds       = 1024
	challengeInterval    = 15 * time.Second
	challengeLearnStep   = 100 * time.Millisecond
	challengeTimeout     = 10 * time.Second
	challengeMaxUnanswer = 3
)

// challengeAnswers maps game hash -> seed -> answer. Guarded by mu.
var (
	challengeAnswers = map[uint64]map[uint32]uint64{}
	challengeSaveMu  sync.Mutex
)

type challengeState struct {
	nextAt     time.Time
	pending    bool
	seed       uint32
	sentAt     time.Time
	unanswered int
	learnSeeds []uint32 // Seeds still to learn (random, picked when learning starts).
	learning   bool
}

// LoadChallenges reads learned answers. A missing file is fine.
func LoadChallenges(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var file map[string]map[string]string
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	for hs, seeds := range file {
		var h uint64
		if _, err := fmt.Sscanf(hs, "%x", &h); err != nil {
			return fmt.Errorf("bad game hash %q", hs)
		}
		m := map[uint32]uint64{}
		for ss, as := range seeds {
			var s uint32
			var a uint64
			if _, err := fmt.Sscanf(ss, "%d", &s); err != nil {
				return fmt.Errorf("bad seed %q", ss)
			}
			if _, err := fmt.Sscanf(as, "%x", &a); err != nil {
				return fmt.Errorf("bad answer %q", as)
			}
			m[s] = a
		}
		challengeAnswers[h] = m
	}
	return nil
}

// saveChallenges writes the learned answers. Caller must hold mu.
func saveChallenges() {
	file := map[string]map[string]string{}
	for h, seeds := range challengeAnswers {
		m := map[string]string{}
		for s, a := range seeds {
			m[fmt.Sprint(s)] = fmt.Sprintf("%016x", a)
		}
		file[fmt.Sprintf("%016x", h)] = m
	}
	data, _ := json.MarshalIndent(file, "", " ")
	path := room.config.ChallengeFile
	go func() {
		challengeSaveMu.Lock()
		defer challengeSaveMu.Unlock()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			logging.Log("Failed to save challenge answers: %v", err)
		}
	}()
}

func challengeLoop() {
	ticker := time.NewTicker(challengeLearnStep)
	defer ticker.Stop()
	for now := range ticker.C {
		mu.Lock()
		for _, a := range aids {
			if a.bot == nil && !a.kicked && a.gameHash != 0 {
				stepChallenge(a, now)
			}
		}
		mu.Unlock()
	}
}

// stepChallenge sends the aid's next challenge when it's due. Caller must hold mu.
func stepChallenge(a *Aid, now time.Time) {
	c := &a.challenge
	if c.pending {
		if now.Sub(c.sentAt) < challengeTimeout {
			return
		}
		c.pending = false
		if !learningChallenges(a) {
			c.unanswered++
			logging.Log("Aid %d didn't answer game hash challenge %d (%d in a row)", a.aid, c.seed, c.unanswered)
			if c.unanswered >= challengeMaxUnanswer && room.config.KickChallengeFails {
				kick(a, KickCheating)
				return
			}
		}
	}
	if now.Before(c.nextAt) {
		return
	}
	known := challengeAnswers[a.gameHash]
	switch {
	case learningChallenges(a):
		if !c.learning {
			c.learning = true
			for range challengeSeeds - len(known) {
				c.learnSeeds = append(c.learnSeeds, rand.Uint32())
			}
		}
		if len(c.learnSeeds) == 0 {
			return
		}
		c.seed = c.learnSeeds[0]
		c.nextAt = now.Add(challengeLearnStep)
	case len(known) == 0:
		return // Nothing learned for this game version: can't check it.
	default:
		seeds := make([]uint32, 0, len(known))
		for s := range known {
			seeds = append(seeds, s)
		}
		c.seed = seeds[rand.IntN(len(seeds))]
		c.nextAt = now.Add(challengeInterval)
	}
	c.pending, c.sentAt = true, now
	packet := []byte{'H', 'C', 0, 0, 0, 0}
	binary.BigEndian.PutUint32(packet[2:], c.seed)
	room.conn.WriteTo(packet, a.addr)
}

func isChallengeReply(data []byte) bool {
	return len(data) == 14 && data[0] == 'H' && data[1] == 'R'
}

// handleChallengeReply checks (or learns) a challenge answer. Caller must hold mu.
func handleChallengeReply(a *Aid, data []byte) {
	c := &a.challenge
	seed := binary.BigEndian.Uint32(data[2:])
	answer := binary.BigEndian.Uint64(data[6:])
	if !c.pending || seed != c.seed {
		return // Late or duplicate.
	}
	c.pending, c.unanswered = false, 0
	if learningChallenges(a) {
		if challengeAnswers[a.gameHash] == nil {
			challengeAnswers[a.gameHash] = map[uint32]uint64{}
		}
		challengeAnswers[a.gameHash][seed] = answer
		if len(c.learnSeeds) > 0 && c.learnSeeds[0] == seed {
			c.learnSeeds = c.learnSeeds[1:]
		}
		if n := len(challengeAnswers[a.gameHash]); n%128 == 0 || len(c.learnSeeds) == 0 {
			saveChallenges()
			logging.Log("Learned %d game hash challenge answers for %016x", len(challengeAnswers[a.gameHash]), a.gameHash)
		}
		return
	}
	want, ok := challengeAnswers[a.gameHash][seed]
	if !ok || want == answer {
		return
	}
	logging.Log("Aid %d failed game hash challenge %d: its code doesn't match game hash %016x", a.aid, seed, a.gameHash)
	if room.config.KickChallengeFails {
		kick(a, KickCheating)
	}
}

// learningChallenges reports whether the aid's answers are learned instead of checked:
// in learn mode, or from a trusted player while their build isn't fully learned.
// Caller must hold mu.
func learningChallenges(a *Aid) bool {
	if room.config.LearnChallenges {
		return true
	}
	return room.config.ChallengeFile != "" && learnsFrom(a) && len(challengeAnswers[a.gameHash]) < challengeSeeds
}
