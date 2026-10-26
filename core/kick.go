package core

import (
	"fmt"
	"net"
	"time"

	"mkw-server/logging"
)

type KickReason byte

const (
	KickInvalidPackets KickReason = iota + 1 // Kept sending packets that failed validation.
	KickRequested                            // wfc-server asked for the kick.
)

func (r KickReason) String() string {
	switch r {
	case KickInvalidPackets:
		return "invalid packets"
	case KickRequested:
		return "requested by wfc-server"
	default:
		return fmt.Sprintf("reason %d", byte(r))
	}
}

// KickHandler is told about every kick, so wfc-server can disconnect the client.
type KickHandler func(addr *net.UDPAddr, aid byte, reason KickReason)

var kickHandler KickHandler

// SetKickHandler registers the function told about kicks. Set it before StartRoom.
func SetKickHandler(h KickHandler) {
	kickHandler = h
}

// strike records an invalid packet from an aid and kicks it once it has sent too many in
// the configured window. The packet itself is always dropped by the caller.
// Caller must hold mu.
func strike(a *Aid, reason string, now time.Time) {
	window := room.config.KickWindow
	kept := a.strikes[:0]
	for _, t := range a.strikes {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	a.strikes = append(kept, now)

	// Log the first strike in a window, then every 10th, to keep wfc-server's log readable.
	if n := len(a.strikes); n == 1 || n%10 == 0 {
		logging.Log("Aid %d dropped packet (%d in last %v): %s", a.aid, n, window, reason)
	}

	if room.config.KickThreshold > 0 && len(a.strikes) >= room.config.KickThreshold {
		kick(a, KickInvalidPackets)
	}
}

// kick stops relaying an aid's packets and tells wfc-server. The aid stays in the map,
// marked kicked, until wfc-server removes it, so its packets keep getting dropped.
// Caller must hold mu.
func kick(a *Aid, reason KickReason) {
	if a.kicked {
		return
	}
	a.kicked = true
	a.readyForCountdown = false
	logging.Log("Kicked aid %d (%s): %s", a.aid, a.addr, reason)

	// Remaining aids may now agree on the countdown state.
	tryUpdateRoomCountdownState()

	if kickHandler != nil {
		addr, aid := a.addr, a.aid
		go kickHandler(addr, aid, reason)
	}
}

// KickAid kicks the aid at addr. Used when wfc-server decides to kick someone.
func KickAid(aidAddr string, reason KickReason) error {
	mu.Lock()
	defer mu.Unlock()

	a := aids[aidAddr]
	if a == nil {
		return fmt.Errorf("Aid %s not in room, can't kick", aidAddr)
	}

	kick(a, reason)
	return nil
}
