package common

// GameMode is the kind of event being played. RaceMode's layout depends on it.
type GameMode byte

const (
	ModeUnknown GameMode = iota
	ModeVS
	ModeBalloonBattle
	ModeCoinRunners
)

func (g GameMode) String() string {
	switch g {
	case ModeVS:
		return "VS"
	case ModeBalloonBattle:
		return "BalloonBattle"
	case ModeCoinRunners:
		return "CoinRunners"
	default:
		return "Unknown"
	}
}

// GameModeFor derives the game mode from a RaceInfo's course and game mode info. Battle
// courses imply a battle; the battle type bit then picks Balloon Battle or Coin Runners.
func GameModeFor(course Course, info GameModeInfo) GameMode {
	switch {
	case course.IsRaceCourse():
		return ModeVS
	case course.IsBattleCourse():
		if info.IsVSTeamOrCoinRunners() {
			return ModeCoinRunners
		}
		return ModeBalloonBattle
	default:
		return ModeUnknown
	}
}
