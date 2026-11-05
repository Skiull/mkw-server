package core

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"mkw-server/logging"
)

// The "Traffic over" log is off for now (too noisy). Set to true to see per-aid traffic.
const logTraffic = false

// How often traffic stats are logged.
const statsInterval = 5 * time.Second

type aidStats struct {
	received int // Datagrams from this aid.
	valid    int // Race packets that passed validation and were relayed.
	sent     int // Packets sent by this bot.
	// Header record lengths of the latest Race packet, to see what phase the client is in.
	lastLengths [8]byte
	// Latest control message (RE, PO, IR).
	lastControl string
}

// logStats logs and resets per-aid traffic counters. Quiet when there was no traffic.
// Caller must hold mu.
func logStats(elapsed time.Duration) {
	var players []string
	botsSent, bots := 0, 0
	for _, a := range aids {
		if a.bot != nil {
			botsSent += a.stats.sent
			bots++
		} else {
			l := a.stats.lastLengths
			players = append(players, fmt.Sprintf(
				"aid %d: %d in, %d valid, records [info %#x mode %#x room/select %#x data %#x user %#x item %#x event %#x], last control %q",
				a.aid, a.stats.received, a.stats.valid, l[1], l[2], l[3], l[4], l[5], l[6], l[7], a.stats.lastControl))
		}
		a.stats = aidStats{lastLengths: a.stats.lastLengths, lastControl: a.stats.lastControl}
	}
	sort.Strings(players)
	if logTraffic {
		logging.Log("Traffic over %v: %s; %d bots sent %d packets", elapsed.Round(time.Second), strings.Join(players, "; "), bots, botsSent)
	}
}
