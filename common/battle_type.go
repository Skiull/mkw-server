package common

type BattleType byte

const (
	// There are other game modes but these are the only valid online ones.
	Balloons BattleType = iota
	Coins
)

func (b BattleType) IsValid() bool {
	return b >= Balloons && b <= Coins
}
