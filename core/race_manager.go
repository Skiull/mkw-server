package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"time"

	"mkw-server/logging"
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

	// All aids have finished the race if we've gotten to this point.
	room.everyoneFinishedRace = true
	onAllAidsFinishedRace()
}

// How long to wait for aids to resend missing frames before clearing race state.
const missingFramesWait = 7 * time.Second

func onAllAidsFinishedRace() {
	// Request aids to send any missing race frames. Clients will stall on the "Please Wait..."
	// screen after race results until the server eihter acks them back (all missing race frames
	// have been resent) or kicks them (failure to resend race frames).
	// TODO: Kicking aids.
	for _, a := range aids {
		if a == nil {
			continue
		}

		if err := a.handleMissingFrames(); err != nil {
			logging.Log("failed to request missing frames from aid %d: %v", a.aid, err)
		}
	}

	// TODO: Ensure this runs after all aids have been acked or kicked. Don't want to
	// reset everything while a race frame is being processed or something.

	time.AfterFunc(missingFramesWait, resetAfterRace)
}

func resetAfterRace() {
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

	if frame.Time >= maxRaceFrames {
		return nil, fmt.Errorf("sent frame.Time (%d) > maxRaceFrames (%d)", frame.Time, maxRaceFrames)
	}

	return &frame, nil
}

func handleRaceFrame(a *Aid, data []byte) error {
	frame, err := parseRaceFrame(data)
	if err != nil {
		return err
	}

	a.raceFrames[frame.Time] = *frame

	// Check if the race frame was resent and handle.
	if missingIdx := slices.Index(a.missingRaceFrames, frame.Time); missingIdx >= 0 {
		if err := a.handleMissingFrameReceived(missingIdx); err != nil {
			logging.Log("aid %d: %v", a.aid, err)
		}
	}

	// Client expects the magic and time as an ack.
	if _, err = room.conn.WriteTo(data[:raceFrameAckSize], a.addr); err != nil {
		return fmt.Errorf("failed to send race frame ack to aid %d: %w", a.aid, err)
	}
	return nil
}
