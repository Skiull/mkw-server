package core

import (
	"encoding/binary"
	"fmt"
	"net"
	"slices"
	"time"

	"mkw-server/logging"
)

// An Aid represents a single client's connection. Can have either one or two players.
type Aid struct {
	conn        net.UDPConn  // Address aids send and receive from
	addr        *net.UDPAddr // The resolved UDP address of this aid
	roomPointer *Room        // The room this aid is in

	sendQueue chan Packet

	aid           byte
	hasGuest      bool
	raceFrameSize int
	players       []*Player
	// Time (in frames) the race results came up. Reset before the start of the next race.
	raceFinishTime uint16

	// Used in place of raceFinishTime in the event of a disconnect.
	lastSentTime uint16

	readyForCountdown bool

	// Used to support countdown logic and the aid's ping control.
	lastPingSent time.Time
	latency      time.Duration
	latencySum   time.Duration
	latencyCount int

	// Distinguishes racers from non-racers. This is important for the countdown.
	isRacer bool

	sentRacePackets   [maxRaceFrames][]byte
	raceFrames        [maxRaceFrames]RaceFrame
	missingRaceFrames []uint16

	raceConfig RaceConfig
	aidConfig  AidConfig
	hasConfig  bool
}

var aids map[string]*Aid = make(map[string]*Aid)

func NewAid(addr string, room *Room, aid byte, hasGuest bool) (*Aid, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to resolve aid address %s: %v", addr, err)
	}

	a := &Aid{
		addr:          udpAddr,
		sendQueue:     make(chan Packet, 32),
		aid:           aid,
		hasGuest:      hasGuest,
		raceFrameSize: calcRaceFrameSize(hasGuest),
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

const missingFramesMagic = 0x4D46
const missingFramesAckMagic = 0x4D41

func (a *Aid) updateMissingFrames() {
	for time, frame := range a.raceFrames {
		// frame.Time being non-zero means we've received it. Continue. This doesn't work for
		// the first frame though, so we instead check the position vector. TODO: Fix this hack.
		if frame.Time != 0 || (time == 0 && a.players[0].frames[frame.Time].Pos != [3]float32{}) {
			continue
		}

		if time >= int(a.raceFinishTime) {
			break
		}

		a.missingRaceFrames = append(a.missingRaceFrames, uint16(time))
	}
}

func (a *Aid) sendMissingFramePacket() error {
	// Structure:
	// 0x0: "MF"
	// 0x2: missingFramesCount
	// 0x4: missingFrames[missingFramesCount]
	buf := make([]byte, 0, 4+2*len(a.missingRaceFrames))
	buf = binary.BigEndian.AppendUint16(buf, missingFramesMagic)
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(a.missingRaceFrames)))
	for _, t := range a.missingRaceFrames {
		buf = binary.BigEndian.AppendUint16(buf, t)
	}

	if _, err := room.conn.WriteTo(buf, a.addr); err != nil {
		return fmt.Errorf("failed to send missing frames to aid %d: %w", a.aid, err)
	}

	logging.Log("requested %d missing frames from aid %d", len(a.missingRaceFrames), a.aid)

	return nil
}

func (a *Aid) handleMissingFrameReceived(i int) error {
	a.missingRaceFrames = slices.Delete(a.missingRaceFrames, i, i+1)

	if len(a.missingRaceFrames) == 0 {
		// Aid is no longer missing frames, send an ack.
		err := a.sendMissingFramesAck()
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *Aid) sendMissingFramesAck() error {
	buf := binary.BigEndian.AppendUint16(nil, missingFramesAckMagic)
	if _, err := room.conn.WriteTo(buf, a.addr); err != nil {
		return fmt.Errorf("failed to send missing frames ack to aid %d: %w", a.aid, err)
	}

	// Try to create the replay file if we aren't missing race frames from anyone
	if !replayFileCreated && haveAllRaceFrames() {
		replayFileCreated = true
		err := createReplayFile()
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *Aid) SetRoomPointer(room *Room) {
	a.roomPointer = room
}

func (a *Aid) GetAddr() string {
	return a.addr.String()
}

func calcRaceFrameSize(hasGuest bool) int {
	var expectedSize int = raceFrameSize + raceFramePlayerSize
	if hasGuest {
		// A guest will have their own RaceFramePlayer record
		expectedSize += raceFramePlayerSize
	}
	return expectedSize
}

func (a *Aid) calcConfigSize() int {
	return configSizeNoPlayers + a.localPlayerCount()*playerConfigSize
}

func (a *Aid) localPlayerCount() int {
	if a.hasGuest {
		return 2
	} else {
		return 1
	}
}
