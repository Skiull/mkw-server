package core

import (
	"fmt"
	"net"
	"time"
)

// An Aid represents a single client's connection. Can have either one or two players.
type Aid struct {
	conn        net.UDPConn  // Address aids send and receive from
	addr        *net.UDPAddr // The resolved UDP address of this aid
	roomPointer *Room        // The room this aid is in

	sendQueue chan Packet

	aid      byte
	hasGuest bool
	players []*Player

	readyForCountdown bool

	// Used to support countdown logic and the aid's ping control.
	lastPingSent time.Time
	latency      time.Duration
	latencySum   time.Duration
	latencyCount int

	// Distinguishes racers from non-racers. This is important for the countdown.
	isRacer bool

	sentRacePackets [maxRaceFrames][]byte
	raceFrames [maxRaceFrames]RaceFrameData
}

var aids map[string]*Aid = make(map[string]*Aid)

func NewAid(addr string, room *Room, aid byte, hasGuest bool) (*Aid, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to resolve aid address %s: %v", addr, err)
	}

	a := &Aid{
		addr:      udpAddr,
		sendQueue: make(chan Packet, 32),
		aid:       aid,
		hasGuest:  hasGuest,
	}

	p, err := newPlayer(aid, false)
	if err != nil {
		return nil, err
	}

	a.players = append(a.players, p)
	if hasGuest {
		p2, err := newPlayer(aid, true)
		if err != nil {
			return nil, err
		}
		a.players = append(a.players, p2)
	}

	return a, nil
}

func (a *Aid) SetRoomPointer(room *Room) {
	a.roomPointer = room
}

func (a *Aid) GetAddr() string {
	return a.addr.String()
}
