package core

import (
	"fmt"

	"mkw-server/logging"
)

const NoIdx = 0xff

func config() (*RaceConfig, error) {
	var firstAid bool = true
	var unanimousRaceConfig RaceConfig
	for _, a := range aids {
		if a == nil || !a.isRacer {
			continue
		}

		aidsRaceConfig := a.raceConfig

		if firstAid {
			firstAid = false
			unanimousRaceConfig = aidsRaceConfig
			continue
		}

		if !unanimousRaceConfig.equals(aidsRaceConfig) {
			return nil, fmt.Errorf("aid %d's raceConfig goes against the unanimous config", a.aid)
		}
	}

	return &unanimousRaceConfig, nil
}

func (r *RaceConfig) equals(r2 RaceConfig) bool {
	return r.Course == r2.Course && r.EngineClass == r2.EngineClass && r.GameMode == r2.GameMode && r.BattleType == r2.BattleType && r.GameModeFlags == r2.GameModeFlags
}

func (a *Aid) createAidRecord(disconnected bool) (*AidRecord, error) {
	var time = a.raceFinishTime
	if disconnected {
		time = a.lastSentTime
	}
	idxToAid := a.idxToAidMap()

	return &AidRecord{
		Aid:              a.aid,
		RaceFinishTime:   time,
		Disconnected:     disconnected,
		LocalPlayerCount: byte(a.localPlayerCount()),
		AidConfig:        a.aidConfig,
		IdxToAidMap:      idxToAid,
		TimeRecvPackets:  a.createTimeRecvPackets(idxToAid),
	}, nil
}

// Create a mapping of slice indexes to aid for quicker lookup
// of the time we received a race packet from a particular aid.
func (a *Aid) idxToAidMap() []byte {
	var out []byte
	for _, aid := range aidsAtRaceStart {
		if aid != a.aid {
			out = append(out, aid)

		}
	}
	return out
}

// Create a mapping of aid to slice index, also for quicker lookup.
func (a *Aid) aidToIdxMap(idxToAid []byte) [12]byte {
	var aidToIdx [12]byte
	for i := range aidToIdx {
		aidToIdx[i] = NoIdx
	}
	for idx, aid := range idxToAid {
		aidToIdx[aid] = byte(idx)
	}
	return aidToIdx
}

func (a *Aid) createTimeRecvPackets(idxToAid []byte) [][]uint16 {
	aidToIdx := a.aidToIdxMap(idxToAid)

	numFrames := len(a.raceFrames)

	timeRecvPackets := make([][]uint16, len(idxToAid))
	for i := range timeRecvPackets {
		timeRecvPackets[i] = make([]uint16, numFrames)
	}

	// Loop over all RaceFrame to get the SentTime of the sending aid.
	for t := 0; t < numFrames; t++ {
		frame := &a.raceFrames[t]
		for senderAid, sentTime := range frame.SentTime {
			idx := aidToIdx[senderAid]
			if idx == NoIdx {
				continue
			}
			timeRecvPackets[idx][t] = sentTime
		}
	}

	return timeRecvPackets
}

func saveDisconnectedAidRecord(a *Aid) {
	r, err := a.createAidRecord(true)
	if err != nil {
		logging.Log("Failed to save/create disconnected aids (%d) AidRecord: %v", a.aid, err)
	}

	r.Disconnected = true
	disconnectedAidRecords = append(disconnectedAidRecords, *r)

	for _, p := range a.players {
		saveDisconnectedPlayerRecord(p, a.lastSentTime)
	}

}

func (p *Player) createPlayerRecord(lastSentTime uint16) PlayerRecord {
	playerRecord := PlayerRecord{
		PlayerId:  p.playerId,
		Aid:       p.aid,
		Config:    p.config,
		RaceFrame: p.frames[:lastSentTime],
	}

	return playerRecord
}

func saveDisconnectedPlayerRecord(p *Player, lastSentTime uint16) {
	record := p.createPlayerRecord(lastSentTime)
	disconnectedPlayersRecords = append(disconnectedPlayersRecords, record)
}
