package core

// Game specific context of a player.
type Player struct {
	playerId byte
	aid      byte
	isGuest  bool

	frames [maxRaceFrames]RaceFramePlayer
}

func newPlayer(aid byte, isGuest bool) (*Player, error) {
	return &Player{
		aid:     aid,
		isGuest: isGuest,
	}, nil
}
