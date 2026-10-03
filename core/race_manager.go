package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// "FT"
const finishTimeMagic = 0x4654

// "RF"
const raceFrameMagic = 0x5246

const finishTimeSize = 4

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

func isFinishTimePacket(data []byte) bool {
	if len(data) != finishTimeSize {
		return false
	}

	return binary.BigEndian.Uint16(data[:2]) == finishTimeMagic
}

func handleFinishTimePacket(a *Aid, data []byte) error {
	if a.raceFinishTime != 0 {
		// aid has already finished, don't do anything.
		return nil
	}

	finishTime := binary.BigEndian.Uint16(data[2:])
	a.raceFinishTime = finishTime

	if _, err := room.conn.WriteTo(binary.BigEndian.AppendUint16(nil, finishTimeMagic), a.addr); err != nil {
		return fmt.Errorf("send ack failed")
	}

	// One aid finished the race, try to update room.allAidsFinishedRace.
	updateRoomFinishState()

	return nil
}

func updateRoomFinishState() {
	racers := 0
	for _, a := range aids {
		if a == nil || !a.isRacer {
			continue
		}
		racers++
		if a.raceFinishTime == 0 {
			return
		}
	}

	if racers == 0 || room.everyoneFinishedRace {
		return
	}

	room.everyoneFinishedRace = true

	onAllAidsFinishedRace()
}

func onAllAidsFinishedRace() {
	// For now, just reset finish times and clear race frame and packet buffers.
	// But we can also use this time to ask aids to resend anything missed.
	for _, a := range aids {
		if a == nil {
			continue
		}
		clear(a.sentRacePackets[:])
		clear(a.raceFrames[:])
		a.raceFinishTime = 0
	}

	room.everyoneFinishedRace = false
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
