package core

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"mkw-server/core/records"
)

// moveRec builds a RaceData record at pos. flags sets bytes by offset (Lakitu 0x27, Bullet
// Bill 0x29, cannon 0x2f-0x30).
func moveRec(t *testing.T, pos [3]float32, flags map[int]byte) *records.RaceData {
	t.Helper()
	d := raceData(1)
	var v [3]uint32
	for i, f := range pos {
		v[i] = (math.Float32bits(f+1e6) - 0x3f800000) & 0x0fffffff
	}
	// x: top 28 bits at 0x0; y: low 28 bits at 0x3; z: top 28 bits at 0x7.
	binary.BigEndian.PutUint32(d[0x0:], v[0]<<4|uint32(d[3]&0x0f))
	y := binary.BigEndian.Uint32(d[0x3:])&0xf0000000 | v[1]
	binary.BigEndian.PutUint32(d[0x3:], y)
	binary.BigEndian.PutUint32(d[0x7:], v[2]<<4|uint32(d[0xa]&0x0f))
	for off, b := range flags {
		d[off] = b
	}
	rd, err := records.ParseRaceData(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := rd.WorldPos(); math.Abs(float64(got[0]-pos[0])) > 1 || math.Abs(float64(got[1]-pos[1])) > 1 || math.Abs(float64(got[2]-pos[2])) > 1 {
		t.Fatalf("encoded %v, decoded %v", pos, got)
	}
	return rd
}

// drive feeds a player positions moving speed u/f along x, one packet every 2 frames, and
// returns the first violation.
func drive(t *testing.T, p *Player, start uint32, frames uint32, x0, speed float32, flags map[int]byte) (string, float32) {
	t.Helper()
	x := x0
	for f := start; f < start+frames; f += 2 {
		x += speed * 2
		if problem := checkMovement(p, f, moveRec(t, [3]float32{x, 100, 500}, flags), nil, false); problem != "" {
			return problem, x
		}
	}
	return "", x
}

func TestMovementNormalDriving(t *testing.T) {
	p := newPlayer(0, false)
	if problem, _ := drive(t, p, 0, 600, -20000, 115, nil); problem != "" {
		t.Fatalf("boosting at 115 u/f flagged: %s", problem)
	}
}

func TestMovementSpeedHack(t *testing.T) {
	p := newPlayer(0, false)
	if problem, _ := drive(t, p, 0, 300, -20000, 200, nil); problem == "" {
		t.Fatal("200 u/f wasn't flagged")
	}
}

func TestMovementTeleport(t *testing.T) {
	p := newPlayer(0, false)
	_, x := drive(t, p, 0, 60, 0, 80, nil)
	if problem := checkMovement(p, 62, moveRec(t, [3]float32{x + 10000, 100, 500}, nil), nil, false); problem == "" {
		t.Fatal("10000 unit jump wasn't flagged")
	}
}

func TestMovementRespawnAllowed(t *testing.T) {
	p := newPlayer(0, false)
	_, x := drive(t, p, 0, 60, 0, 80, nil)
	lakitu := map[int]byte{0x27: 1 << 6}
	checkMovement(p, 62, moveRec(t, [3]float32{x, 100, 500}, lakitu), nil, false)
	// Lakitu puts the player back far away.
	if problem := checkMovement(p, 64, moveRec(t, [3]float32{x - 15000, 100, 500}, lakitu), nil, false); problem != "" {
		t.Fatalf("Lakitu respawn flagged: %s", problem)
	}
	if problem, _ := drive(t, p, 66, 120, x-15000, 80, nil); problem != "" {
		t.Fatalf("driving after a respawn flagged: %s", problem)
	}
}

func TestMovementBulletBillAndCannon(t *testing.T) {
	p := newPlayer(0, false)
	_, x := drive(t, p, 0, 60, 0, 80, nil)
	// Using a Bullet Bill flips its bit and allows ~145 u/f.
	if problem, _ := drive(t, p, 60, 300, x, 145, map[int]byte{0x29: 1 << 5}); problem != "" {
		t.Fatalf("Bullet Bill flagged: %s", problem)
	}

	p = newPlayer(0, false)
	_, x = drive(t, p, 0, 60, 0, 80, nil)
	// A cannon flips the cannon bits; the flight can be very fast.
	cannon := map[int]byte{0x2f: 0, 0x30: 1 << 6}
	if problem, _ := drive(t, p, 60, 240, x, 400, cannon); problem != "" {
		t.Fatalf("cannon flight flagged: %s", problem)
	}
}

// Before the kart is placed, positions are all zero; the first real one isn't a teleport.
func TestMovementIgnoresUnplacedKart(t *testing.T) {
	p := newPlayer(0, false)
	d := raceData(1)
	unplaced, err := records.ParseRaceData(d)
	if err != nil {
		t.Fatal(err)
	}
	for f := uint32(0); f < 20; f += 2 {
		if problem := checkMovement(p, f, unplaced, nil, false); problem != "" {
			t.Fatalf("unplaced kart flagged: %s", problem)
		}
	}
	if problem, _ := drive(t, p, 20, 120, -21000, 60, nil); problem != "" {
		t.Fatalf("first real position flagged: %s", problem)
	}
}

func TestMovementMega(t *testing.T) {
	// Mega on a fast bike (2.6x of 120), plus a conveyor or current pushing along.
	p := newPlayer(0, false)
	x := float32(-30000)
	for f := uint32(0); f < 300; f += 2 {
		x += 310 * 2
		if problem := checkMovement(p, f, moveRec(t, [3]float32{x, 100, 500}, nil), nil, true); problem != "" {
			t.Fatalf("Mega at 310 u/f flagged: %s", problem)
		}
	}
	p = newPlayer(0, false)
	if problem, _ := drive(t, p, 0, 300, -20000, 310, nil); problem == "" {
		t.Fatal("310 u/f without a Mega wasn't flagged")
	}
}

func TestMovementLagGapNotTeleport(t *testing.T) {
	// Packets stop for 40 frames (lag) while driving at 115 u/f: 4600 units, not a teleport.
	p := newPlayer(0, false)
	_, x := drive(t, p, 0, 60, 0, 115, nil)
	if problem := checkMovement(p, 98, moveRec(t, [3]float32{x + 115*40, 100, 500}, nil), nil, false); problem != "" {
		t.Fatalf("lag gap flagged: %s", problem)
	}
}

func TestBulletBillEndsWhenSlowingDown(t *testing.T) {
	now := time.Now()
	p := newPlayer(0, false)
	p.bulletAt = now
	_, x := drive(t, p, 0, 60, -30000, 100, nil)
	bullet := map[int]byte{0x29: 1 << 5}
	_, x = drive(t, p, 60, 120, x, 145, bullet)
	if !p.invincible(now) {
		t.Fatal("Bullet Bill ended while flying")
	}

	// Stuck on a wall for a moment, then flying on: back to active, no speed flag.
	_, x = drive(t, p, 180, 40, x, 20, bullet)
	if p.invincible(now) {
		t.Fatal("stuck Bullet Bill still counted as active")
	}
	problem, x := drive(t, p, 220, 120, x, 145, bullet)
	if problem != "" {
		t.Fatalf("Bullet Bill flying on after a wall bump flagged: %s", problem)
	}
	if !p.invincible(now) {
		t.Fatal("Bullet Bill flying on not counted as active again")
	}

	// Really over: stays at driving speed.
	drive(t, p, 340, 90, x, 100, bullet)
	if p.invincible(now) {
		t.Fatal("Bullet Bill still active after slowing to driving speed")
	}
}

func TestMovementKickOnFirstViolation(t *testing.T) {
	setupRoom(t, Config{EnforceMovement: true})
	a := addTestAid(t, 0, false)
	if !movementViolation(a, a.players[0], 0, "test", time.Now()) || !a.kicked {
		t.Fatal("not kicked on the first violation")
	}
}

func TestMegaLimitPerVehicle(t *testing.T) {
	m := &moveState{}
	if got := megaSpeedLimit(m); got != maxMegaSpeed {
		t.Fatalf("no stats: %v, want %v", got, float32(maxMegaSpeed))
	}
}

func TestSquishLimit(t *testing.T) {
	m := &moveState{squishSince: 100}
	if _, ok := squishLimit(m, 110, 140); ok {
		t.Fatal("limit applied before the speed could drop")
	}
	limit, ok := squishLimit(m, 140, 170)
	if !ok || limit < 84 || limit > 110 {
		t.Fatalf("limit %v %v", limit, ok)
	}
	if _, ok := squishLimit(m, 300, 400); ok {
		t.Fatal("limit applied after the squish ended")
	}
}

func TestStarEndsSquish(t *testing.T) {
	p := newPlayer(0, false)
	_, x := drive(t, p, 0, 60, -30000, 80, nil)
	squished := map[int]byte{0x2a: 1}
	_, x = drive(t, p, 60, 60, x, 70, squished)
	if p.move.squishSince == 0 {
		t.Fatal("squish not seen")
	}
	// Star while squished: back to full speed, no squish line.
	star := map[int]byte{0x2a: 1 | 1<<7}
	drive(t, p, 120, 120, x, 115, star)
	if p.move.squishSince != 0 || p.move.squishNote != "" {
		t.Fatalf("squish still applied after a Star: %q", p.move.squishNote)
	}
}

func TestComboSpeedLimit(t *testing.T) {
	m := &moveState{}
	if _, ok := comboSpeedLimit(m, 1000, false); ok {
		t.Fatal("limit without stats")
	}
}
