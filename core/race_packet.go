package core

import (
	"encoding/binary"
	"fmt"

	"mkw-server/common"
	"mkw-server/core/records"
)

// mkw-server's prefix in the Header's magic field: [0] = RacePacketMagic, [1] = sender aid,
// [2:4] = bitmap of aids the packet should be relayed to.
const racePacketSenderOffset = 1
const racePacketDestOffset = 2

type RacePacket struct {
	Header   *records.Header
	RaceInfo *records.RaceInfo
	RaceMode *records.RaceMode
	Room     *records.Room
	Select   *records.Select
	RaceData []*records.RaceData
	User     *records.User
	Items    []*records.ItemState
	Event    *records.Event

	SenderAid    byte
	Destinations uint16
}

// parseRacePacket parses and validates every record in a Race packet. Records are laid out
// back to back in a fixed order, with their lengths given by the Header.
func parseRacePacket(data []byte, ctx *records.Context) (*RacePacket, error) {
	// All Race packets must have a Header record. The Header record is always 0x10 bytes.
	if len(data) < records.HeaderLen {
		return nil, fmt.Errorf("invalid size, must be >= %#x. Actual is %#x", records.HeaderLen, len(data))
	}

	if data[0] != RacePacketMagic {
		return nil, fmt.Errorf("invalid magic (%#x)", data[0])
	}

	senderAid := data[racePacketSenderOffset]
	if senderAid != ctx.SenderAid {
		return nil, fmt.Errorf("claimed aid (%d) != sender aid (%d)", senderAid, ctx.SenderAid)
	}

	// Only real aids, and never the sender itself.
	destinations := binary.BigEndian.Uint16(data[racePacketDestOffset : racePacketDestOffset+2])
	if destinations>>records.MaxAids != 0 {
		return nil, fmt.Errorf("destination bitmap has invalid aids (%#x)", destinations)
	}
	if destinations&(1<<senderAid) != 0 {
		return nil, fmt.Errorf("destination bitmap includes sender (%#x)", destinations)
	}

	header, err := records.ParseHeader(data)
	if err != nil {
		return nil, fmt.Errorf("Header parsing failure: %w", err)
	}

	p := &RacePacket{
		Header:       header,
		SenderAid:    senderAid,
		Destinations: destinations,
	}

	offset := records.HeaderLen
	next := func(n byte) []byte {
		rec := data[offset : offset+int(n)]
		offset += int(n)
		return rec
	}

	if n := header.RaceInfoLen(); n != 0 {
		p.RaceInfo, err = records.ParseRaceInfo(next(n))
		if err != nil {
			return nil, fmt.Errorf("RaceInfo parsing failure: %w", err)
		}
	}

	if n := header.RaceModeLen(); n != 0 {
		// RaceMode's layout depends on the game mode, which comes from RaceInfo.
		mode, isRacer := common.ModeUnknown, false
		if p.RaceInfo != nil {
			mode, isRacer = p.RaceInfo.GameMode(), p.RaceInfo.IsRacer()
		}
		p.RaceMode, err = records.ParseRaceMode(next(n), ctx, mode, isRacer)
		if err != nil {
			return nil, fmt.Errorf("RaceMode parsing failure: %w", err)
		}
	}

	switch n := header.RoomSelectLen(); n {
	case 0:
	case records.RoomLen:
		p.Room, err = records.ParseRoom(next(n), ctx)
		if err != nil {
			return nil, fmt.Errorf("Room parsing failure: %w", err)
		}
	case records.SelectLen:
		p.Select, err = records.ParseSelect(next(n), ctx)
		if err != nil {
			return nil, fmt.Errorf("Select parsing failure: %w", err)
		}
	}

	if n := header.RaceDataLen(); n != 0 {
		p.RaceData, err = records.ParseRaceDataRecords(next(n), ctx)
		if err != nil {
			return nil, fmt.Errorf("RaceData parsing failure: %w", err)
		}
	}

	if n := header.UserLen(); n != 0 {
		p.User, err = records.ParseUser(next(n), ctx)
		if err != nil {
			return nil, fmt.Errorf("User parsing failure: %w", err)
		}
	}

	if n := header.ItemLen(); n != 0 {
		p.Items, err = records.ParseItemRecords(next(n), ctx)
		if err != nil {
			return nil, fmt.Errorf("Item parsing failure: %w", err)
		}
	}

	if n := header.EventLen(); n != 0 {
		p.Event, err = records.ParseEvent(next(n))
		if err != nil {
			return nil, fmt.Errorf("Event parsing failure: %w", err)
		}
	}

	// ParseHeader already checks the record lengths add up to the packet length.
	if offset != len(data) {
		return nil, fmt.Errorf("parsed %d bytes of %d", offset, len(data))
	}

	return p, nil
}

// IsCountdownStarted reports whether the sender's countdown has begun.
func (p *RacePacket) IsCountdownStarted() bool {
	return p.RaceInfo != nil && p.RaceInfo.CountdownElapsedTime() != 0
}
