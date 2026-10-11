package core

import (
	"mkw-server/common"
)

const version = 0

const replayFileMagic = 0x52504c59 // "RPLY"

type ReplayFileHeader struct {
	Magic         uint32
	CRC32         uint32
	Version       uint16
	AidCount      byte
	PlayerCount   byte
	Course        common.Course
	EngineClass   common.EngineClass
	GameMode      common.GameMode
	BattleType    common.BattleType
	GameModeFlags common.GameModeFlags
}

var header ReplayFileHeader

func createReplayFile() error {
	h, err := createReplayFileHeader()
	if err != nil {
		return err
	}

	header = *h

	return nil
}

func createReplayFileHeader() (*ReplayFileHeader, error) {
	config, err := config()
	if err != nil {
		return nil, err
	}

	return &ReplayFileHeader{
		Magic:         replayFileMagic,
		Version:       version,
		AidCount:      numAidsAtRaceStart,
		PlayerCount:   numPlayersAtRaceStart,
		Course:        config.Course,
		EngineClass:   config.EngineClass,
		GameMode:      config.GameMode,
		BattleType:    config.BattleType,
		GameModeFlags: config.GameModeFlags,
	}, nil
}
