package core

import (
	"fmt"
	"net"
	"time"
)

const RacePacketMagic uint8 = 0xb

type Packet struct {
	sender       net.Addr
	aid          *Aid
	racePacket   *RacePacket
	data         []byte
	receivedTime time.Time
}

func isRacePacket(data []byte) bool {
	// Not the best way to check if this packet is a race packet, starting with 0xb is a weak magic.
	return len(data) >= 16 && data[0] == RacePacketMagic
}

func handleRacePacket(aid *Aid, data []byte) error {
	racePacket, err := parseRacePacket(data)
	if err != nil {
		return err
	}

	raceInfo := racePacket.RaceInfo
	if raceInfo == nil {
		return nil
	}

	// We only want to try to save client records if they've started the countdown.
	// We also want to ignore race packets containing a User record. User records are only
	// useful for players not currently in the race (ex, someone joining a public room), serves
	// no purpose for verification.
	if raceInfo.StartedRace() && racePacket.Header.UserLen() == 0 {
		time := raceInfo.Time()
		if time > maxRaceFrames {
			return fmt.Errorf("time (%d) > maxRaceFrames (%d)", time, maxRaceFrames)
		}
		aid.sentRacePackets[time] = data
	}

	return nil
}

// TODO: Rewrite when Race packet parsing is implemented.
func containsSelect(data []byte) bool {
	// Anything less than 0x10 is invalid. All Race packets must contain a header of 0x10.
	if len(data) < 0x10 {
		// TODO: Move this sort of check to somewhere before this function gets called.
		return false
	}

	// Offset 0xb is the Select Record record size field in the Header record.
	// Select record size is always 0x38.
	return data[0xb] == 0x38
}
