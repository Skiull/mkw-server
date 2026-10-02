package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// "RF"
const raceFrameMagic = 0x5246

// 60 frames per second, 60 seconds per minute, 6 minutes max per race.
const maxRaceFrames = 60 * 60 * 6
const raceFrameAckSize = 4
const raceFramePacketSize = 0x44

type RaceFrameData struct {
	Time     uint16
	Inputs   uint32
	Pos      [3]float32
	SentTime [12]uint32
}

func isRaceFramePacket(magic uint16) bool {
	return magic == raceFrameMagic
}

func parseRaceFrame(data []byte) (*RaceFrameData, error) {
	if len(data) != raceFramePacketSize {
		return nil, fmt.Errorf("len(data) (%d) < raceFramePacketSize (%d)", len(data), raceFramePacketSize)
	}

	magic := binary.BigEndian.Uint16(data[:2])
	if magic != raceFrameMagic {
		return nil, fmt.Errorf("bad magic (%#x != %#x)", magic, raceFrameMagic)
	}

	var frame RaceFrameData
	if err := binary.Read(bytes.NewReader(data[2:]), binary.BigEndian, &frame); err != nil {
		return nil, fmt.Errorf("Read() error: %w", err)
	}

	return &frame, nil
}

func handleRaceFrame(a *Aid, data []byte) error {
	frame, err := parseRaceFrame(data)
	if err != nil {
		return err
	}
	a.raceFrames[frame.Time] = *frame

	// Client expects the magic and time as an ack.
	if _, err = room.conn.WriteTo(data[:raceFrameAckSize], a.addr); err != nil {
		return fmt.Errorf("failed to send race frame ack to aid %d: %w", a.aid, err)
	}
	return nil
}
