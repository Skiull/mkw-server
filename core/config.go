package core

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"mkw-server/common"
)

// "CF"
const configMagic = 0x4346

// "CA"
const configAckMagic = 0x4341

const raceConfigSize = 0x5

type RaceConfig struct {
	Course      common.Course
	EngineClass common.EngineClass
	GameMode    common.GameMode
	BattleType  common.BattleType
	ModeFlags   common.GameModeFlags
}

const rflWifiDataSize = 0xa0
const aidConfigSize = rflWifiDataSize + 0x1

type AidConfig struct {
	RegionLine  common.RegionLine
	RFLWifiData [rflWifiDataSize]byte
}

const playerConfigSize = 0xc

type PlayerConfig struct {
	PlayerId     byte
	Aid          byte
	Character    common.Character
	Vehicle      common.Vehicle
	Team         common.Team
	StartingSpot byte
	DriftType    common.DriftType
	RankIcon     common.RankIcon
	Points       uint16
	Rating       uint16
}

const configSizeNoPlayers = 0x2 + raceConfigSize + aidConfigSize

type Config struct {
	Magic uint16
	Race  RaceConfig
	Aid   AidConfig
}

func isConfigPacket(data []byte) bool {
	return binary.BigEndian.Uint16(data[:2]) == configMagic
}

func handleConfigPacket(a *Aid, data []byte) error {
	r := bytes.NewReader(data)

	var config Config
	if err := binary.Read(r, binary.BigEndian, &config); err != nil {
		return fmt.Errorf("failed to read config config: %w", err)
	}

	a.raceConfig = config.Race
	a.regionLine = config.Aid.RegionLine
	a.rflWifiData = config.Aid.RFLWifiData
	a.hasConfig = true

	playerConfigs := make([]PlayerConfig, a.localPlayerCount())
	if err := binary.Read(r, binary.BigEndian, playerConfigs); err != nil {
		return fmt.Errorf("failed to read player configs: %w", err)
	}

	for i, p := range a.players {
		p.config = playerConfigs[i]
	}

	// All aids must send a config for the countdown to start, try to update the countdown state.
	tryUpdateRoomCountdownState()

	if _, err := room.conn.WriteTo(binary.BigEndian.AppendUint16(nil, configAckMagic), a.addr); err != nil {
		return fmt.Errorf("failed to send config ack: %w", err)
	}
	return nil
}

func haveAllAidsSentConfig() bool {
	for _, a := range aids {
		if a == nil || !a.isRacer {
			continue
		}
		if !a.hasConfig {
			return false
		}
	}
	return true
}
