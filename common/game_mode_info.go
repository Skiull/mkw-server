package common

import (
	"fmt"
)

const teamVSOrCoinRunnersMask = 0x80000000

// RaceInfo's team bitfield is one bit per player.
const raceInfoTeamsMask = 0x00000fff

// Select's is two bits wide (for some reason).
const selectTeamsMask = 0x00ffffff

type GameModeInfo struct {
	isVSTeamOrCoinRunners bool
	// Indexed by playerId. 0 indicates blue team, 1 denotes red team.
	teamsBitfield uint32
}

func ParseGameModeInfo(data uint32, r RecordIdx) (GameModeInfo, error) {
	isVSTeamOrCoinRunners := data&teamVSOrCoinRunnersMask != 0

	var teamsMask uint32
	if r == RaceInfo {
		teamsMask = raceInfoTeamsMask
	} else if r == RoomSelect {
		teamsMask = selectTeamsMask
	} else {
		return GameModeInfo{}, fmt.Errorf("invalid recordIdx (%d)", r)
	}

	teamsBitfield := data & teamsMask

	return GameModeInfo{
		isVSTeamOrCoinRunners: isVSTeamOrCoinRunners,
		teamsBitfield:         teamsBitfield,
	}, nil
}

func (g GameModeInfo) IsVSTeamOrCoinRunners() bool {
	return g.isVSTeamOrCoinRunners
}

func (g GameModeInfo) TeamsBitfield() uint32 {
	return g.teamsBitfield
}
