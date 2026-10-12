package core

import (
	"slices"

	"mkw-server/common"
	"mkw-server/logging"
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

type AidRecord struct {
	Aid              byte
	RaceFinishTime   uint16
	LocalPlayerCount byte
	Disconnected     bool
	AidConfig        AidConfig
	IdxToAidMap      []byte
	TimeRecvPackets  [][]uint16
}

type PlayerRecord struct {
	PlayerId  byte
	Aid       byte
	Config    PlayerConfig
	RaceFrame []RaceFramePlayer
}

var header ReplayFileHeader
var aidRecords []AidRecord
var playerRecords []PlayerRecord
var disconnectedAidRecords []AidRecord
var disconnectedPlayersRecords []PlayerRecord

func createReplayFile() error {
	logging.Log("Creating replay file!")
	h, err := createReplayFileHeader()
	if err != nil {
		return err
	}

	header = *h

	err = createAidRecords()
	if err != nil {
		return err
	}

	err = createPlayerRecords()
	if err != nil {
		return err
	}

	// TODO: Actually call this function async after file has been created.
	onReplayFileCreated()

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
		AidCount:      byte(len(aidsAtRaceStart)),
		PlayerCount:   numPlayersAtRaceStart,
		Course:        config.Course,
		EngineClass:   config.EngineClass,
		GameMode:      config.GameMode,
		BattleType:    config.BattleType,
		GameModeFlags: config.GameModeFlags,
	}, nil
}

func createAidRecords() error {
	logging.Log("Starting to create aidRecords!")

	// Clear out all aid records except for the disconnected records.
	aidRecords = append(aidRecords[:0], disconnectedAidRecords...)

	for _, a := range aids {
		if a == nil || !slices.Contains(aidsAtRaceStart, a.aid) {
			continue
		}

		aidRecord, err := a.createAidRecord(false)
		if err != nil {
			return err
		}
		aidRecords = append(aidRecords, *aidRecord)
	}

	logging.Log("Done creating %d aid records!", len(aidRecords))

	return nil
}

func createPlayerRecords() error {
	logging.Log("Creating player records!")

	// Clear the non-disconnected player records
	playerRecords = append(playerRecords[:0], disconnectedPlayersRecords...)

	for _, a := range aids {
		if a == nil || !slices.Contains(aidsAtRaceStart, a.aid) {
			continue
		}

		for _, p := range a.players {
			record := p.createPlayerRecord(a.raceFinishTime)
			playerRecords = append(playerRecords, record)
		}
	}

	logging.Log("Finished creating %d Player records", len(playerRecords))

	return nil
}

func onReplayFileCreated() {
	numPlayersAtRaceStart = 0
	aidsAtRaceStart = aidsAtRaceStart[:0]
	aidRecords = aidRecords[:0]
	disconnectedAidRecords = disconnectedAidRecords[:0]
	playerRecords = playerRecords[:0]
	disconnectedPlayersRecords = disconnectedPlayersRecords[:0]
}
