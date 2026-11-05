package common

type Course byte

const (
	MarioCircuit Course = iota
	MooMooMeadows
	MushroomGorge
	GrumbleVolcano
	ToadsFactory
	CoconutMall
	DKSummit
	WariosGoldMine
	LuigiCircuit
	DaisyCircuit
	MoonviewHighway
	MapleTreeway
	BowsersCastle
	RainbowRoad
	DryDryRuins
	KoopaCape
	RPeachBeach
	RMarioCircuit
	RWaluigiStadium
	RDKMountain
	RYoshiFalls
	RDesertHills
	RPeachGardens
	RDelfinoSquare
	RMarioCircuit3
	RGhostValley2
	RMarioRaceway
	RSherbetLand
	RBowsersCastle
	RDKJungleParkway
	RBC3
	RShyGuyBeach
	DelfinoPier
	BlockPlaza
	ChainChompRoulette
	FunkyStadium
	ThwompDesert
	GCNCookieLand
	DSTwilightHouse
	SNESBattleCourse4
	GBABattleCourse3
	N64Skyscraper
	// Only found in Select records.
	Random Course = 0x43
	NoVote Course = 0xff
)

func (c Course) isCourse() bool {
	return c >= MarioCircuit && c <= N64Skyscraper
}

// RaceInfo's coursePlayed won't contain NoVote (0x43) or Random (0xff) but a Select record can.
// So we have to have a different implementation of an "IsValid()" depending on the record type.
func (c Course) IsValid(r RecordIdx) bool {
	if r == RaceInfo {
		return c.isCourse()
	} else if r == RoomSelect {
		return c.isCourse() || c == Random || c == NoVote
	}
	return false
}

// IsRaceCourse reports whether c is one of the 32 race courses.
func (c Course) IsRaceCourse() bool {
	return c >= MarioCircuit && c <= RShyGuyBeach
}

// IsBattleCourse reports whether c is one of the 10 battle arenas.
func (c Course) IsBattleCourse() bool {
	return c >= DelfinoPier && c <= N64Skyscraper
}
