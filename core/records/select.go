package records

import (
	"encoding/binary"
	"fmt"

	"mkw-server/common"
)

const SelectLen = 0x38

type SelectPlayerInfo struct {
	spot      uint16
	points    uint16
	character common.Character
	vehicle   common.Vehicle
	vote      common.Course
	rankIcon  common.RankIcon
}

type Select struct {
	timeSender       uint64
	timeReceiver     uint64
	p1Info           *SelectPlayerInfo
	p2Info           *SelectPlayerInfo
	seed             uint32
	gameModeInfo     common.GameModeInfo
	playerIdToAidMap [12]byte
	selectedCourse   common.Course
	phase            common.SelectPhase
	winningAid       byte
	engineClass      common.EngineClass
}

// A player's rank in the previous race. 0 before the first race.
const maxSelectRank = 12

func parseSelectPlayer(data []byte) (*SelectPlayerInfo, error) {
	spot := binary.BigEndian.Uint16(data[0x0:0x2])
	if spot > maxSelectRank {
		return nil, fmt.Errorf("invalid spot (%d)", spot)
	}

	// Accumulated event points. Any value is reachable over a long enough event.
	points := binary.BigEndian.Uint16(data[0x2:0x4])

	character := common.Character(data[0x4])
	if !character.IsValid(common.RoomSelect) {
		return nil, fmt.Errorf("invalid character (%d)", int(character))
	}

	vehicle := common.Vehicle(data[0x5])
	if !vehicle.IsValid(common.RoomSelect) {
		return nil, fmt.Errorf("invalid vehicle (%d)", int(vehicle))
	}

	vote := common.Course(data[0x6])
	if !vote.IsValid(common.RoomSelect) {
		return nil, fmt.Errorf("invalid vote (%d)", vote)
	}

	rankIcon := common.RankIcon(data[0x7])
	if !rankIcon.IsValid() {
		return nil, fmt.Errorf("invalid rankIcon (%d)", int(rankIcon))
	}

	return &SelectPlayerInfo{
		spot:      spot,
		points:    points,
		character: character,
		vehicle:   vehicle,
		vote:      vote,
		rankIcon:  rankIcon,
	}, nil
}

// Without a guest, player 2's fields hold fixed default values.
func (p *SelectPlayerInfo) isEmptyGuest() bool {
	return p.spot == 0 && p.points == 0 &&
		p.character == common.SelectDefaultCharacter &&
		p.vehicle == common.SelectDefaultVehicle &&
		p.vote == common.Random
}

func ParseSelect(data []byte, ctx *Context) (*Select, error) {
	if len(data) != SelectLen {
		return nil, fmt.Errorf("size mismatch (%d != %d)", len(data), SelectLen)
	}

	timeSender := binary.BigEndian.Uint64(data[0x0:0x8])
	timeReceiver := binary.BigEndian.Uint64(data[0x8:0x10])

	p1Info, err := parseSelectPlayer(data[0x10:0x18])
	if err != nil {
		return nil, err
	}

	p2Info, err := parseSelectPlayer(data[0x18:0x20])
	if err != nil {
		return nil, err
	}

	if ctx.hasGuest() {
		// Both players on a console vote together.
		if p2Info.vote != p1Info.vote {
			return nil, fmt.Errorf("guest vote (%d) != player 1 vote (%d)", p2Info.vote, p1Info.vote)
		}
	} else if !p2Info.isEmptyGuest() {
		return nil, fmt.Errorf("guest data present without a guest (%+v)", *p2Info)
	}

	seed := binary.BigEndian.Uint32(data[0x20:0x24])

	gameModeInfoRaw := binary.BigEndian.Uint32(data[0x24:0x28])
	gameModeInfo, err := common.ParseGameModeInfo(gameModeInfoRaw, common.RoomSelect)
	if err != nil {
		return nil, fmt.Errorf("invalid gameModeInfo: %v", err)
	}

	playerIdToAidMap := [12]byte(data[0x28:0x34])
	if err := validatePlayerIdToAidMap(playerIdToAidMap); err != nil {
		return nil, err
	}

	selectedCourse := common.Course(data[0x34])
	if !selectedCourse.IsValid(common.RoomSelect) {
		return nil, fmt.Errorf("selectedCourse invalid (%d)", selectedCourse)
	}

	phase := common.SelectPhase(data[0x35])
	if !phase.IsValid() {
		return nil, fmt.Errorf("phase invalid (%d)", phase)
	}

	winningAid := data[0x36]
	// NoAid is the default value for winningAid when the track hasn't yet been decided.
	if winningAid != NoAid && !ctx.aidInRoom(winningAid) {
		return nil, fmt.Errorf("winningAid invalid (%d)", winningAid)
	}

	engineClass := common.EngineClass(data[0x37])
	if !engineClass.IsValid() {
		return nil, fmt.Errorf("invalid engineClass (%d)", engineClass)
	}

	return &Select{
		timeSender:       timeSender,
		timeReceiver:     timeReceiver,
		p1Info:           p1Info,
		p2Info:           p2Info,
		seed:             seed,
		gameModeInfo:     gameModeInfo,
		playerIdToAidMap: playerIdToAidMap,
		selectedCourse:   selectedCourse,
		phase:            phase,
		winningAid:       winningAid,
		engineClass:      engineClass,
	}, nil
}

// validatePlayerIdToAidMap checks a player id -> aid map. Each entry is an aid or NoAid,
// and an aid can own at most two players (itself and a guest).
func validatePlayerIdToAidMap(m [12]byte) error {
	var counts [MaxAids]int
	for playerId, aid := range m {
		if aid == NoAid {
			continue
		}
		if aid >= MaxAids {
			return fmt.Errorf("player %d mapped to invalid aid (%d)", playerId, aid)
		}
		counts[aid]++
		if counts[aid] > 2 {
			return fmt.Errorf("aid %d mapped to more than 2 players", aid)
		}
	}
	return nil
}
