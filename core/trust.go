package core

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"mkw-server/logging"
)

// Trusted players (-trusted-players): their clients are known to be clean, so whatever
// they report is learned instead of checked:
//   - their in-room game hash joins the allowlist (Config.GameHashes);
//   - their login game hash (reported in the room as 'G' 'L' + u64) is saved for
//     wfc-server's login check;
//   - their game hash challenge answers are learned while a build has fewer than
//     challengeSeeds of them;
//   - StaticR.rel .data blocks they change join the normal list (MutableDataBlocks).
// Learned values go to -learn-file (JSON), which wfc-server also reads for login hashes.
//
// Trust comes from the profile ID wfc-server sends when the player joins (its login was
// verified there), not from anything the client claims.

type learnedFile struct {
	GameHashes        []string `json:"gameHashes"`
	LoginHashes       []string `json:"loginHashes"`
	MutableDataBlocks []int    `json:"mutableDataBlocks"`
}

// Guarded by mu.
var (
	learnedLoginHashes = map[uint64]bool{}
	learnSaveMu        sync.Mutex
)

// ParseTrustedPlayers reads friend codes (1234-5678-9012) or profile IDs.
func ParseTrustedPlayers(list string) (map[uint32]bool, error) {
	out := map[uint32]bool{}
	for _, item := range strings.Split(list, ",") {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, item)
		if digits == "" {
			continue
		}
		v, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad trusted player %q", item)
		}
		// A friend code holds the profile ID in its low 32 bits.
		out[uint32(v)] = true
	}
	return out, nil
}

// LoadLearned merges a learn file into config (a missing file is fine). Call before
// StartRoom.
func LoadLearned(path string, config *Config) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f learnedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	for _, h := range f.GameHashes {
		v, err := strconv.ParseUint(h, 16, 64)
		if err != nil {
			return fmt.Errorf("bad game hash %q", h)
		}
		if config.GameHashes == nil {
			config.GameHashes = map[uint64]bool{}
		}
		config.GameHashes[v] = true
	}
	for _, h := range f.LoginHashes {
		if v, err := strconv.ParseUint(h, 16, 64); err == nil {
			learnedLoginHashes[v] = true
		}
	}
	if config.MutableDataBlocks == nil {
		config.MutableDataBlocks = map[int]bool{}
	}
	for _, b := range f.MutableDataBlocks {
		config.MutableDataBlocks[b] = true
	}
	return nil
}

// SetAidProfile records the logged-in profile ID wfc-server gave for an aid.
func SetAidProfile(aidAddr string, profileID uint32) {
	mu.Lock()
	defer mu.Unlock()
	if a := aids[aidAddr]; a != nil {
		a.profileID = profileID
		if isTrusted(a) {
			logging.Log("Aid %d is trusted player %d: learning its game hashes, challenges and data blocks", a.aid, profileID)
		}
	}
}

func isTrusted(a *Aid) bool {
	return a.profileID != 0 && room.config.TrustedPlayers[a.profileID]
}

// learnGameHash adds a trusted player's in-room game hash. Caller must hold mu.
func learnGameHash(a *Aid, hash uint64) {
	if room.config.GameHashes == nil {
		room.config.GameHashes = map[uint64]bool{}
	}
	if room.config.GameHashes[hash] {
		return
	}
	room.config.GameHashes[hash] = true
	logging.Log("Learned game hash %016x from trusted aid %d", hash, a.aid)
	saveLearned()
}

func isLoginHashPacket(data []byte) bool {
	return len(data) == 10 && data[0] == 'G' && data[1] == 'L'
}

// handleLoginHash learns a trusted player's login game hash. Caller must hold mu.
func handleLoginHash(a *Aid, data []byte) {
	hash := binary.BigEndian.Uint64(data[2:])
	if !isTrusted(a) || learnedLoginHashes[hash] {
		return
	}
	learnedLoginHashes[hash] = true
	logging.Log("Learned login game hash %016x from trusted aid %d", hash, a.aid)
	saveLearned()
}

// learnDataBlocks adds .data blocks a trusted player changed. Caller must hold mu.
func learnDataBlocks(a *Aid, blocks []int) {
	var added []int
	for _, b := range blocks {
		if !room.config.MutableDataBlocks[b] {
			if room.config.MutableDataBlocks == nil {
				room.config.MutableDataBlocks = map[int]bool{}
			}
			room.config.MutableDataBlocks[b] = true
			added = append(added, b)
		}
	}
	if len(added) > 0 {
		logging.Log("Learned normal .data blocks %s from trusted aid %d", blockList(added), a.aid)
		saveLearned()
	}
}

// saveLearned writes the learn file. Caller must hold mu.
func saveLearned() {
	path := room.config.LearnFile
	if path == "" {
		return
	}
	var f learnedFile
	for h := range room.config.GameHashes {
		f.GameHashes = append(f.GameHashes, fmt.Sprintf("%016x", h))
	}
	for h := range learnedLoginHashes {
		f.LoginHashes = append(f.LoginHashes, fmt.Sprintf("%016x", h))
	}
	for b := range room.config.MutableDataBlocks {
		f.MutableDataBlocks = append(f.MutableDataBlocks, b)
	}
	slices.Sort(f.GameHashes)
	slices.Sort(f.LoginHashes)
	slices.Sort(f.MutableDataBlocks)
	data, _ := json.MarshalIndent(f, "", " ")
	go func() {
		learnSaveMu.Lock()
		defer learnSaveMu.Unlock()
		// Merge with what other rooms (separate processes) saved meanwhile.
		if old, err := os.ReadFile(path); err == nil {
			var prev learnedFile
			if json.Unmarshal(old, &prev) == nil {
				f.GameHashes = union(f.GameHashes, prev.GameHashes)
				f.LoginHashes = union(f.LoginHashes, prev.LoginHashes)
				f.MutableDataBlocks = union(f.MutableDataBlocks, prev.MutableDataBlocks)
				data, _ = json.MarshalIndent(f, "", " ")
			}
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err == nil {
			os.Rename(tmp, path)
		}
	}()
}

func union[T string | int](a, b []T) []T {
	seen := map[T]bool{}
	var out []T
	for _, v := range append(a, b...) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}
