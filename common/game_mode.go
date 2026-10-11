package common

type GameMode byte

type GameModeFlags byte

const (
	// There are other game modes but these are the only valid online ones.
	PrivateVS GameMode = 7 + iota
	PublicVS
	PrivateBattle
	PublicBattle
)

const (
	// There may be some other flags, these are the documented once at time of writing.
	Mirror GameModeFlags = 1 << iota
	Teams                = 1 << iota
)

func (g GameMode) IsValid() bool {
	return g >= PrivateVS && g <= PublicBattle
}
