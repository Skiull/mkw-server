package records

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
)

const UserLen = 0xc0

const miiLen = 0x4c
const userMaxMiiCount = 2

// The game caps VR and BR at 9999.
const maxRankPoints = 9999

// Region values in the User record.
const maxRegion = 5

type User struct {
	PresentMiis   uint32
	Miis          [userMaxMiiCount][miiLen]byte
	WiiFriendCode uint64
	FriendCode    uint64
	Country       byte
	State         byte
	Longitude     uint16
	Latitude      uint16
	VR            uint16
	BR            uint16
	GameRegion    byte // Fourth character of the game id (E, P, J or K).
	Region        byte
}

func isValidGameRegion(c byte) bool {
	switch c {
	case 'E', 'P', 'J', 'K':
		return true
	default:
		return false
	}
}

// IsValidFriendCode checks a Wii friend code's checksum. The upper bits of a friend code
// are derived from the profile id and game id, so a made-up friend code fails this check.
// Mirrors wfc-server's common.CalcFriendCode.
func IsValidFriendCode(fc uint64, gameId string) bool {
	pid := uint32(fc)
	if pid == 0 || len(gameId) != 4 {
		return false
	}

	buffer := make([]byte, 8)
	binary.LittleEndian.PutUint32(buffer, pid)
	buffer[4] = gameId[3]
	buffer[5] = gameId[2]
	buffer[6] = gameId[1]
	buffer[7] = gameId[0]

	digest := md5.Sum(buffer)
	return fc == uint64(pid)|(uint64(digest[0]&0xfe)<<31)
}

func ParseUser(data []byte, ctx *Context) (*User, error) {
	if len(data) != UserLen {
		return nil, fmt.Errorf("size mismatch (%d != %d)", len(data), UserLen)
	}

	u := &User{
		PresentMiis:   binary.BigEndian.Uint32(data[0x0:0x4]),
		WiiFriendCode: binary.BigEndian.Uint64(data[0xa0:0xa8]),
		FriendCode:    binary.BigEndian.Uint64(data[0xa8:0xb0]),
		Country:       data[0xb0],
		State:         data[0xb1],
		Longitude:     binary.BigEndian.Uint16(data[0xb4:0xb6]),
		Latitude:      binary.BigEndian.Uint16(data[0xb6:0xb8]),
		VR:            binary.BigEndian.Uint16(data[0xb8:0xba]),
		BR:            binary.BigEndian.Uint16(data[0xba:0xbc]),
		GameRegion:    data[0xbc],
		Region:        data[0xbd],
	}
	for i := range userMaxMiiCount {
		start := 0x8 + i*miiLen
		u.Miis[i] = [miiLen]byte(data[start : start+miiLen])
	}

	// One bit per Mii, so at most one bit per local player.
	if u.PresentMiis>>userMaxMiiCount != 0 {
		return nil, fmt.Errorf("invalid present Miis bitfield (%#x)", u.PresentMiis)
	}
	if !ctx.hasGuest() && u.PresentMiis&0b10 != 0 {
		return nil, fmt.Errorf("guest Mii present without a guest (%#x)", u.PresentMiis)
	}

	maxMiiCount := binary.BigEndian.Uint16(data[0x4:0x6])
	if maxMiiCount != userMaxMiiCount {
		return nil, fmt.Errorf("max Mii count (%d) != %d", maxMiiCount, userMaxMiiCount)
	}

	if unk := binary.BigEndian.Uint16(data[0x6:0x8]); unk != 0 {
		return nil, fmt.Errorf("unknown 0x6 (%#x) != 0", unk)
	}

	if !isValidGameRegion(u.GameRegion) {
		return nil, fmt.Errorf("invalid game region (%#x)", u.GameRegion)
	}

	if u.Region > maxRegion {
		return nil, fmt.Errorf("invalid region (%d)", u.Region)
	}

	if u.VR > maxRankPoints {
		return nil, fmt.Errorf("VR (%d) > %d", u.VR, maxRankPoints)
	}

	if u.BR > maxRankPoints {
		return nil, fmt.Errorf("BR (%d) > %d", u.BR, maxRankPoints)
	}

	if !IsValidFriendCode(u.FriendCode, "RMC"+string(u.GameRegion)) {
		return nil, fmt.Errorf("invalid friend code (%d)", u.FriendCode)
	}

	if unk := binary.BigEndian.Uint16(data[0xbe:0xc0]); unk != 0 {
		return nil, fmt.Errorf("unknown 0xbe (%#x) != 0", unk)
	}

	return u, nil
}
