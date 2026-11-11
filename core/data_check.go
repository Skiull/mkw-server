package core

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"mkw-server/logging"
)

// StaticR.rel .data checks. The client fingerprints the REL's .data (game tables,
// constants and some state) in 1 KiB blocks right after loading, and after every code hash
// pass reports which blocks have changed since:
//
//	'G' 'D' + block count (u16, big endian) + a bit per block, most significant first
//
// Normal play changes some blocks; those are listed in Config.MutableDataBlocks. A change
// in any other block means something wrote to game data that never changes on its own, like
// a cheat code poking a value after the game loaded.

const dataChangesMagic = "GD"

func isDataChangesPacket(data []byte) bool {
	return len(data) >= 4 && string(data[:2]) == dataChangesMagic
}

// parseDataChanges returns the changed block numbers, or false if the packet is malformed.
func parseDataChanges(data []byte) ([]int, bool) {
	count := int(data[2])<<8 | int(data[3])
	if len(data) != 4+(count+7)/8 {
		return nil, false
	}
	var changed []int
	for b := range count {
		if data[4+b/8]&(0x80>>(b%8)) != 0 {
			changed = append(changed, b)
		}
	}
	return changed, true
}

// Caller must hold mu.
func handleDataChanges(a *Aid, data []byte, now time.Time) {
	changed, ok := parseDataChanges(data)
	if !ok {
		strike(a, "malformed .data report", now)
		return
	}
	if isTrusted(a) {
		learnDataBlocks(a, changed)
	}
	var unexpected []int
	for _, b := range changed {
		if !room.config.MutableDataBlocks[b] || cheatDataBlocks[b] {
			unexpected = append(unexpected, b)
		}
	}
	key := fmt.Sprint(changed)
	if key != a.dataChangesSeen {
		a.dataChangesSeen = key
		logging.Log("Aid %d StaticR.rel .data blocks changed since load: %s (not on the normal list: %s)",
			a.aid, blockList(changed), blockList(unexpected))
	}
	if len(unexpected) > 0 && room.config.KickDataCheats {
		logging.Log("Aid %d game data modified after loading (blocks %s)", a.aid, blockList(unexpected))
		kick(a, KickCheating)
	}
}

func blockList(blocks []int) string {
	if len(blocks) == 0 {
		return "none"
	}
	parts := make([]string, len(blocks))
	for i, b := range slices.Sorted(slices.Values(blocks)) {
		parts[i] = fmt.Sprint(b)
	}
	return strings.Join(parts, ",")
}
