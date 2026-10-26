package core

import (
	"fmt"
	"net"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
)

// An Aid represents a single client's connection. Can have either one or two players.
type Aid struct {
	addr *net.UDPAddr // The resolved UDP address of this aid

	aid      byte
	isHost   bool
	hasGuest bool
	players  []*Player

	readyForCountdown bool

	// Used to support countdown logic and the aid's ping control.
	lastPingSent time.Time
	latency      time.Duration
	latencySum   time.Duration
	latencyCount int

	// Distinguishes racers from non-racers. This is important for the countdown.
	isRacer bool

	// Friend code from the aid's first User record. A client can't change identity mid-session.
	friendCode uint64

	// Times of recent invalid packets, used to decide when to kick.
	strikes []time.Time
	kicked  bool

	// Game mode and whether the race is underway, from the latest RaceInfo.
	mode        common.GameMode
	raceStarted bool

	// Previous packet's EVENT slot bytes, to spot newly added item events.
	lastEventSlots [records.EventSlotCount]byte
	hasEventSlots  bool
}

// aids is keyed by UDP address. Guarded by mu.
var aids = make(map[string]*Aid)

func NewAid(addr string, aid byte, isHost bool, hasGuest bool) (*Aid, error) {
	if aid >= records.MaxAids {
		return nil, fmt.Errorf("invalid aid %d", aid)
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve aid address %s: %w", addr, err)
	}

	a := &Aid{
		addr:     udpAddr,
		aid:      aid,
		isHost:   isHost,
		hasGuest: hasGuest,
	}

	a.players = append(a.players, newPlayer(aid, false))
	if hasGuest {
		a.players = append(a.players, newPlayer(aid, true))
	}

	return a, nil
}

func (a *Aid) GetAddr() string {
	return a.addr.String()
}

func (a *Aid) localPlayerCount() int {
	return len(a.players)
}

// context is what packet validation knows about this aid. Caller must hold mu.
func (a *Aid) context() *records.Context {
	return &records.Context{
		SenderAid:        a.aid,
		LocalPlayerCount: a.localPlayerCount(),
		IsHost:           a.isHost,
		IsAidInRoom:      isAidInRoom,
	}
}
