package records

import (
	"encoding/binary"
	"fmt"

	"mkw-server/common"
)

const RaceModeLen = 0x28

// A guest slot that isn't in use is reported as client slot 0xC in RaceMode.
const raceModeNoSlot = 0xc

// Upper bounds the game itself clamps these timers to.
const maxSlowFrames = 300    // Frames since last moving faster than 30km/h.
const maxIdleFrames = 3600   // Frames since each countdown value maxed out.
const maxMilliseconds = 1000 // Milliseconds field of a finishing time.
const maxSeconds = 60        // Seconds field of a finishing time.

// FinishTime is a race finishing time as packed in RaceMode (6 bit minutes, 6 bit seconds,
// 10 bit milliseconds).
type FinishTime struct {
	Minutes      byte
	Seconds      byte
	Milliseconds uint16
}

func (t FinishTime) isValid() bool {
	return t.Seconds < maxSeconds && t.Milliseconds < maxMilliseconds
}

func parseFinishTime(raw uint32) FinishTime {
	return FinishTime{
		Minutes:      byte(raw >> 16 & 0x3f),
		Seconds:      byte(raw >> 10 & 0x3f),
		Milliseconds: uint16(raw & 0x3ff),
	}
}

// RaceModeVS is the VS race layout of a RaceMode record.
type RaceModeVS struct {
	FinishedPlayers     uint16
	ParticipatingPlayer uint16
	DisconnectedPlayers uint16
	LocalPlayerCount    byte
	Slots               [2]byte
	FinishTimes         [2]FinishTime
	PlayersAhead        [2]uint16
	TimeSinceLeader     uint32
	MinTimeout          uint32
	Disconnected        bool
	SlowFrames          [2]uint16
	IdleFrames          [2]uint16
}

// RaceModeBattle is the part of the Balloon Battle layout that can be validated.
type RaceModeBattle struct {
	Slots  [2]byte
	Scores [2]byte
}

// RaceMode contains game-mode specific data. VS races and Balloon Battles are parsed and
// validated. Coin Runners' layout isn't known, so only its raw bytes are kept.
type RaceMode struct {
	Mode   common.GameMode
	VS     *RaceModeVS
	Battle *RaceModeBattle
	data   [RaceModeLen]byte
}

// ParseRaceMode parses a RaceMode record. mode comes from the RaceInfo in the same packet.
// isRacer is false for spectators, whose player fields aren't filled in.
func ParseRaceMode(data []byte, ctx *Context, mode common.GameMode, isRacer bool) (*RaceMode, error) {
	if len(data) != RaceModeLen {
		return nil, fmt.Errorf("size mismatch (%d != %d)", len(data), RaceModeLen)
	}

	r := &RaceMode{
		Mode: mode,
		data: [RaceModeLen]byte(data),
	}

	var err error
	switch mode {
	case common.ModeVS:
		r.VS, err = parseRaceModeVS(data, ctx, isRacer)
	case common.ModeBalloonBattle:
		r.Battle, err = parseRaceModeBattle(data, ctx, isRacer)
	}
	if err != nil {
		return nil, err
	}

	return r, nil
}

func parseRaceModeVS(data []byte, ctx *Context, isRacer bool) (*RaceModeVS, error) {
	b := newBitReader(data)
	vs := &RaceModeVS{}
	vs.FinishedPlayers = uint16(b.read(13))
	vs.ParticipatingPlayer = uint16(b.read(13))
	vs.DisconnectedPlayers = uint16(b.read(13))
	vs.LocalPlayerCount = byte(b.read(2))
	for i := range 2 {
		vs.Slots[i] = byte(b.read(4))
		vs.FinishTimes[i] = parseFinishTime(b.read(22))
		vs.PlayersAhead[i] = uint16(b.read(13))
	}

	vs.TimeSinceLeader = binary.BigEndian.Uint32(data[0x14:0x18])
	vs.MinTimeout = binary.BigEndian.Uint32(data[0x18:0x1c])

	disconnected := data[0x1c]
	if disconnected > 1 {
		return nil, fmt.Errorf("invalid disconnected flag (%d)", disconnected)
	}
	vs.Disconnected = disconnected == 1

	for i := range 2 {
		vs.SlowFrames[i] = binary.BigEndian.Uint16(data[0x1e+2*i:])
		if vs.SlowFrames[i] > maxSlowFrames {
			return nil, fmt.Errorf("slow frames for player %d too large (%d)", i+1, vs.SlowFrames[i])
		}

		vs.IdleFrames[i] = binary.BigEndian.Uint16(data[0x22+2*i:])
		if vs.IdleFrames[i] > maxIdleFrames {
			return nil, fmt.Errorf("idle frames for player %d too large (%d)", i+1, vs.IdleFrames[i])
		}
	}

	if !isRacer {
		return vs, nil
	}

	// A client can't report more players than it has, and every player it reports must be
	// its own. Otherwise it could finish a race or disconnect someone else.
	if int(vs.LocalPlayerCount) > ctx.LocalPlayerCount {
		return nil, fmt.Errorf("local player count (%d) > actual (%d)", vs.LocalPlayerCount, ctx.LocalPlayerCount)
	}

	for i := range int(vs.LocalPlayerCount) {
		if vs.Slots[i] != ctx.SenderAid {
			return nil, fmt.Errorf("player %d slot (%d) != sender aid (%d)", i+1, vs.Slots[i], ctx.SenderAid)
		}

		if !vs.FinishTimes[i].isValid() {
			return nil, fmt.Errorf("invalid finish time for player %d (%+v)", i+1, vs.FinishTimes[i])
		}
	}

	return vs, nil
}

func parseRaceModeBattle(data []byte, ctx *Context, isRacer bool) (*RaceModeBattle, error) {
	battle := &RaceModeBattle{
		Slots:  [2]byte{data[0] >> 4, data[0] & 0xf},
		Scores: [2]byte{byte(readBits(data, 8, 7)), byte(readBits(data, 15, 7))},
	}

	if !isRacer {
		return battle, nil
	}

	if battle.Slots[0] != ctx.SenderAid {
		return nil, fmt.Errorf("player 1 slot (%d) != sender aid (%d)", battle.Slots[0], ctx.SenderAid)
	}

	if ctx.hasGuest() {
		if battle.Slots[1] != ctx.SenderAid {
			return nil, fmt.Errorf("player 2 slot (%d) != sender aid (%d)", battle.Slots[1], ctx.SenderAid)
		}
	} else if battle.Slots[1] != raceModeNoSlot || battle.Scores[1] != 0 {
		return nil, fmt.Errorf("guest data present without a guest (slot %d, score %d)", battle.Slots[1], battle.Scores[1])
	}

	return battle, nil
}
