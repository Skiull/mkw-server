package core

import (
	"fmt"
)

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
