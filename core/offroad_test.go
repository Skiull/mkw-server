package core

import (
	"testing"

	"mkw-server/items"
)

// driveFlat drives along x at y=100 (above the test floor) like drive, with flags.
func offroadDrive(t *testing.T, p *Player, start, frames uint32, x0, speed float32, flags map[int]byte) (string, float32) {
	t.Helper()
	return drive(t, p, start, frames, x0, speed, flags)
}

func TestOffroadSpeed(t *testing.T) {
	p := newPlayer(0, false)
	p.move.floors = items.NewTestFloors(items.FloorOffroad, 100000)

	// Entering offroad at full speed: momentum carries for a while, no flag.
	if problem, _ := offroadDrive(t, p, 0, 50, -30000, 115, nil); problem != "" {
		t.Fatalf("entering offroad flagged: %s", problem)
	}
	// Staying at 115 u/f on offroad with no boost: flagged.
	if problem, _ := offroadDrive(t, p, 50, 200, -20000, 115, nil); problem == "" {
		t.Fatal("full speed on offroad wasn't flagged")
	}

	// Legit offroad speed (38% of 120 = 46) is fine.
	p = newPlayer(0, false)
	p.move.floors = items.NewTestFloors(items.FloorOffroad, 100000)
	if problem, _ := offroadDrive(t, p, 0, 300, -30000, 44, nil); problem != "" {
		t.Fatalf("offroad speed flagged: %s", problem)
	}

	// Mushroom boost flag held: full speed is fine.
	p = newPlayer(0, false)
	p.move.floors = items.NewTestFloors(items.FloorOffroad, 100000)
	shroom := map[int]byte{0x29: 1 << 6}
	if problem, _ := offroadDrive(t, p, 0, 300, -30000, 115, shroom); problem != "" {
		t.Fatalf("Mushroom on offroad flagged: %s", problem)
	}
}

func TestWeakOffroadIsLenient(t *testing.T) {
	p := newPlayer(0, false)
	p.move.floors = items.NewTestFloors(items.FloorWeakOffroad, 100000)
	// 60% of 120 = 72.
	if problem, _ := offroadDrive(t, p, 0, 300, -30000, 72, nil); problem != "" {
		t.Fatalf("weak offroad speed flagged: %s", problem)
	}
}
