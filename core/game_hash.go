package core

import (
	"encoding/binary"
	"time"

	"mkw-server/logging"
)

// Game hash checks. The client hashes its game code and read-only data (main.dol and
// StaticR.rel, after its own patches) a slice per frame and sends each finished pass:
//
//	'G' 'H' + 8 byte hash (big endian)
//
// Cheat codes patch code or constants, so a cheating client's hash isn't one of the known
// good ones. A modified client could still send a good hash: this stops casual cheating
// (Gecko codes, Dolphin cheats), not someone rewriting the client.

const gameHashMagic = "GH"
const gameHashLen = 10

// A pass can straddle the moment code gets patched (logging in), so a hash only counts
// against a player after this many unknown passes in a row.
const unknownHashesBeforeKick = 2

func isGameHashPacket(data []byte) bool {
	return len(data) == gameHashLen && string(data[:2]) == gameHashMagic
}

// Caller must hold mu.
func handleGameHash(a *Aid, data []byte, now time.Time) {
	hash := binary.BigEndian.Uint64(data[2:])
	known := room.config.GameHashes == nil || room.config.GameHashes[hash]

	if hash != a.gameHash {
		status := "known"
		switch {
		case room.config.GameHashes == nil:
			status = "no allowlist set"
		case !known:
			status = "UNKNOWN"
		}
		logging.Log("Aid %d game hash %016x (%s)", a.aid, hash, status)
		a.gameHash = hash
	}

	if known {
		a.unknownHashes = 0
		return
	}
	a.unknownHashes++
	if a.unknownHashes >= unknownHashesBeforeKick && room.config.KickHashCheats {
		logging.Log("Aid %d game code doesn't match any known client version (%016x)", a.aid, hash)
		kick(a, KickBadVersion)
	}
}
