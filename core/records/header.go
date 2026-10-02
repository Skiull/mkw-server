package records

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const HeaderLen = 0x10

// TODO: Move non-header record lenghts to their respective source files.
const RaceDataLen = 0x40
const UserLen = 0xc0
const ItemLen = 0x8
const EventMinLen = 0x18
const EventMaxLen = 0xf8

type Header struct {
	magic         uint32
	crc32         uint32
	headerLen     byte
	raceInfoLen   byte
	raceModeLen   byte
	roomSelectLen byte
	raceDataLen   byte
	userLen       byte
	itemLen       byte
	eventLen      byte
}

func ParseHeader(data []byte) (*Header, error) {
	// TODO: Verify magic after packet extension is implemented.

	crc := binary.BigEndian.Uint32(data[4:8])
	if crc == 0 {
		return nil, errors.New("checksum is 0")
	}

	// crc calculation doesn't include the crc itself. Clear it now, restore after check.
	clear(data[4:8])

	calcCRC := crc32.ChecksumIEEE(data)
	if crc != calcCRC {
		return nil, fmt.Errorf("crc != calcCRC (%d != %d)", crc, calcCRC)
	}

	binary.BigEndian.PutUint32(data[4:8], calcCRC)

	headerLen := data[8]
	if headerLen != HeaderLen {
		return nil, fmt.Errorf("headerLen (%d) != %d", headerLen, HeaderLen)
	}

	// Header is the only record that *must* exist. All other records may or may not exist.
	raceInfoLen := data[9]
	if raceInfoLen != 0 && raceInfoLen != RaceInfoLen {
		return nil, fmt.Errorf("raceInfoLen (%d) != (0, %d)", raceInfoLen, RaceInfoLen)
	}

	raceModeLen := data[10]
	if raceModeLen != 0 && raceModeLen != RaceModeLen {
		return nil, fmt.Errorf("raceModeLen (%d) != (0, %d)", raceModeLen, RaceModeLen)
	}

	roomSelectLen := data[11]
	// Room and Select record lengths are both stored at this offset. Only one is present at once.
	if roomSelectLen != 0 && roomSelectLen != RoomLen && roomSelectLen != SelectLen {
		return nil, fmt.Errorf("roomSelectLen (%d) != (0, %d, %d)", roomSelectLen, RoomLen, SelectLen)
	}

	raceDataLen := data[12]
	// Either not present or one per local player.
	if raceDataLen != 0 && raceDataLen != RaceDataLen && raceDataLen != (RaceDataLen*2) {
		return nil, fmt.Errorf("raceDataLen (%d) != (0, %d)", raceDataLen, RaceDataLen)
	}

	userLen := data[13]
	if userLen != 0 && userLen != UserLen {
		return nil, fmt.Errorf("userLen (%d) != (0, %d)", userLen, UserLen)
	}

	itemLen := data[14]
	// Either not present or one per local player.
	if itemLen != 0 && itemLen != ItemLen && itemLen != (ItemLen*2) {
		return nil, fmt.Errorf("itemLen (%d) != (0, %d)", itemLen, ItemLen)
	}

	eventLen := data[15]
	// Event can vary in record length.
	if eventLen != 0 && !(eventLen >= EventMinLen && eventLen <= EventMaxLen) {
		return nil, fmt.Errorf("eventLen (%d) != 0 && %d <= eventLen <= %d", eventLen, EventMinLen, EventMaxLen)
	}

	header := &Header{
		magic:         binary.BigEndian.Uint32(data[:4]),
		crc32:         calcCRC,
		headerLen:     headerLen,
		raceInfoLen:   raceInfoLen,
		raceModeLen:   raceModeLen,
		roomSelectLen: roomSelectLen,
		raceDataLen:   raceDataLen,
		userLen:       userLen,
		itemLen:       itemLen,
		eventLen:      eventLen,
	}

	if header.length() != len(data) {
		return nil, fmt.Errorf("header.length() != len(data) (%d != %d)", header.length(), len(data))
	}

	return header, nil
}

func (h *Header) length() int {
	return int(h.headerLen) + int(h.raceInfoLen) + int(h.raceModeLen) + int(h.roomSelectLen) + int(h.raceDataLen) + int(h.userLen) + int(h.itemLen) + int(h.eventLen)
}

func (h *Header) RaceInfoLen() byte {
	return h.raceInfoLen
}

func (h *Header) RaceModeLen() byte {
	return h.raceModeLen
}

func (h *Header) RoomSelectLen() byte {
	return h.roomSelectLen
}

func (h *Header) UserLen() byte {
	return h.userLen
}
