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
//
// Only the newest client build is accepted. Each build reports its build number (build.py:
// the commit time) in its 'G' 'L' packet. When a trusted player shows up with a higher one,
// every game hash, login hash and challenge answer of older builds is forgotten, so players
// on an older build are refused until they update. Learning needs the build number, so
// trusted players on builds from before build numbers don't teach anything.
// Learned values go to -learn-file (JSON), which wfc-server also reads for login hashes.
//
// Trust comes from the profile ID wfc-server sends when the player joins (its login was
// verified there), not from anything the client claims.

type learnedFile struct {
	Build             uint32   `json:"build"` // Build number the hashes below belong to.
	GameHashes        []string `json:"gameHashes"`
	LoginHashes       []string `json:"loginHashes"`
	MutableDataBlocks []int    `json:"mutableDataBlocks"`
	// Blocks known cheats write: never learned or treated as normal, even from a trusted
	// player testing a cheat.
	CheatDataBlocks []int `json:"cheatDataBlocks"`
}

// StaticR.rel .data blocks known cheats write: 256 bytes each from the section start (USA:
// 0x808AE520, 679 blocks). Of the codes in host/cheat-tests, only Instant MT writes .data;
// the others patch code (caught by the game hash) or run their own. The rest are from the
// Gecko code database (codes.rc24.xyz RMCE01/RMCP01); PAL .data starts 0x48C0 later.
var builtinCheatDataBlocks = []int{
	43,     // MegaMushroom lasts for ever [mdmwii] (PAL 80 8B59C4, USA 80 8B1104)
	44,     // Bullet Bill Speed Mod 2 [mdmwii] (USA 80 8B1138)
	45, 46, // Instant Mini Turbo + Stand-Still MT (80 8B1278, 80 8B1400-08)
	// Not listed: Gravity Modifier writes block 55, which normal play changes too.
}

// Guarded by mu.
var cheatDataBlocks = map[int]bool{}

func init() {
	for _, b := range builtinCheatDataBlocks {
		cheatDataBlocks[b] = true
	}
}

// dropCheatBlocks takes known cheat blocks off the normal list.
func dropCheatBlocks(m map[int]bool) {
	for b := range cheatDataBlocks {
		delete(m, b)
	}
}

// Guarded by mu.
var (
	learnedLoginHashes = map[uint64]bool{}
	currentBuild       uint32
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
	currentBuild = f.Build
	if config.GameHashes == nil {
		// An empty allowlist still means "check": only learned builds get in.
		config.GameHashes = map[uint64]bool{}
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
	for _, b := range f.CheatDataBlocks {
		cheatDataBlocks[b] = true
	}
	for _, b := range f.MutableDataBlocks {
		config.MutableDataBlocks[b] = true
	}
	dropCheatBlocks(config.MutableDataBlocks)
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

// learnsFrom reports whether the server learns what this aid reports: a trusted player on
// the current build. Caller must hold mu.
func learnsFrom(a *Aid) bool {
	return isTrusted(a) && a.buildID != 0 && a.buildID == currentBuild
}

// setBuild records an aid's build number and, for a trusted player on a newer build than
// any learned, makes it the only accepted build. Caller must hold mu.
func setBuild(a *Aid, build uint32) {
	if build == 0 || build == a.buildID {
		return
	}
	a.buildID = build
	if !isTrusted(a) || build <= currentBuild {
		if isTrusted(a) && build < currentBuild {
			logging.Log("Trusted aid %d is on old build %d (current %d): not learning from it", a.aid, build, currentBuild)
		}
		return
	}
	logging.Log("Trusted aid %d is on new build %d (was %d): older builds are no longer accepted", a.aid, build, currentBuild)
	currentBuild = build
	room.config.GameHashes = map[uint64]bool{}
	learnedLoginHashes = map[uint64]bool{}
	challengeAnswers = map[uint64]map[uint32]uint64{}
	for _, other := range aids {
		other.challenge = challengeState{}
	}
	if room.config.ChallengeFile != "" {
		saveChallenges()
	}
	saveLearned()
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

// 'G' 'L' + login hash (u64), then the build number (u32) on builds that report one.
func isLoginHashPacket(data []byte) bool {
	return (len(data) == 10 || len(data) == 14) && data[0] == 'G' && data[1] == 'L'
}

// handleLoginHash notes the aid's build and learns a trusted player's login game hash.
// Caller must hold mu.
func handleLoginHash(a *Aid, data []byte) {
	hash := binary.BigEndian.Uint64(data[2:10])
	if len(data) == 14 {
		setBuild(a, binary.BigEndian.Uint32(data[10:]))
	}
	if !learnsFrom(a) {
		return
	}
	// The in-room hash arrives just before this packet, so learn it now rather than
	// counting it as unknown.
	if a.gameHash != 0 {
		learnGameHash(a, a.gameHash)
		a.unknownHashes = 0
	}
	if learnedLoginHashes[hash] {
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
		if cheatDataBlocks[b] {
			logging.Log("Not learning .data block %d from trusted aid %d: a known cheat writes it", b, a.aid)
			continue
		}
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
	f := learnedFile{Build: currentBuild}
	for h := range room.config.GameHashes {
		f.GameHashes = append(f.GameHashes, fmt.Sprintf("%016x", h))
	}
	for h := range learnedLoginHashes {
		f.LoginHashes = append(f.LoginHashes, fmt.Sprintf("%016x", h))
	}
	for b := range room.config.MutableDataBlocks {
		if !cheatDataBlocks[b] {
			f.MutableDataBlocks = append(f.MutableDataBlocks, b)
		}
	}
	for b := range cheatDataBlocks {
		f.CheatDataBlocks = append(f.CheatDataBlocks, b)
	}
	slices.Sort(f.CheatDataBlocks)
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
				if prev.Build > f.Build {
					return // Another room already moved to a newer build.
				}
				if prev.Build == f.Build {
					f.GameHashes = union(f.GameHashes, prev.GameHashes)
					f.LoginHashes = union(f.LoginHashes, prev.LoginHashes)
				}
				// Normal .data blocks don't depend on the build: always keep them.
				f.CheatDataBlocks = union(f.CheatDataBlocks, prev.CheatDataBlocks)
				cheat := map[int]bool{}
				for _, b := range f.CheatDataBlocks {
					cheat[b] = true
				}
				f.MutableDataBlocks = slices.DeleteFunc(union(f.MutableDataBlocks, prev.MutableDataBlocks), func(b int) bool { return cheat[b] })
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
