package common

type EngineClass byte

const (
	// 50CC isn't in online gameplay but has a value of 0 for offline gameplay. In online, spectators
	// don't set the engine class in their RaceInfo records, leaving a default value of 0.

	// DefaultEngineClass can be found in both RaceInfo and Select records.
	DefaultEngineClass EngineClass = iota
	CC100
	CC150
	Battle
)

func (e EngineClass) IsValid() bool {
	return e >= DefaultEngineClass && e <= Battle
}
