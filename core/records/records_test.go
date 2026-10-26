package records

import (
	"crypto/md5"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"mkw-server/common"
)

func soloCtx() *Context {
	return &Context{SenderAid: 3, LocalPlayerCount: 1, IsAidInRoom: func(aid byte) bool { return aid < 4 }}
}

func guestCtx() *Context {
	c := soloCtx()
	c.LocalPlayerCount = 2
	return c
}

func expectErr(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected error containing %q, got %q", contains, err)
	}
}

// ---- Select ----

func validSelect(guest bool) []byte {
	d := make([]byte, SelectLen)
	// Player 1: spot 0, points 0, Mario, Standard Kart M, votes Luigi Circuit, no icon.
	d[0x14], d[0x15], d[0x16], d[0x17] = byte(common.Mario), byte(common.StandardKartM), byte(common.LuigiCircuit), 0
	if guest {
		d[0x1c], d[0x1d], d[0x1e] = byte(common.Luigi), byte(common.StandardKartS), byte(common.LuigiCircuit)
	} else {
		d[0x1c], d[0x1d], d[0x1e] = byte(common.SelectDefaultCharacter), byte(common.SelectDefaultVehicle), byte(common.Random)
	}
	for i := range 12 {
		d[0x28+i] = NoAid
	}
	d[0x28], d[0x29] = 3, 1
	d[0x34] = byte(common.NoVote)
	d[0x36] = NoAid
	return d
}

func TestSelectValid(t *testing.T) {
	if _, err := ParseSelect(validSelect(false), soloCtx()); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSelect(validSelect(true), guestCtx()); err != nil {
		t.Fatal(err)
	}
}

// gameModeInfo lives at 0x24. It used to be read from 0x8 (inside timeReceiver).
func TestSelectGameModeInfoOffset(t *testing.T) {
	d := validSelect(false)
	binary.BigEndian.PutUint64(d[0x8:0x10], 0xffffffffffffffff)
	binary.BigEndian.PutUint32(d[0x24:0x28], 0x80000003)
	s, err := ParseSelect(d, soloCtx())
	if err != nil {
		t.Fatal(err)
	}
	if !s.gameModeInfo.IsVSTeamOrCoinRunners() || s.gameModeInfo.TeamsBitfield() != 3 {
		t.Fatalf("gameModeInfo read from wrong offset: %+v", s.gameModeInfo)
	}
}

func TestSelectGuestDataWithoutGuest(t *testing.T) {
	_, err := ParseSelect(validSelect(true), soloCtx())
	expectErr(t, err, "guest data present without a guest")
}

func TestSelectGuestVoteMismatch(t *testing.T) {
	d := validSelect(true)
	d[0x1e] = byte(common.MooMooMeadows)
	_, err := ParseSelect(d, guestCtx())
	expectErr(t, err, "guest vote")
}

func TestSelectInvalidSpot(t *testing.T) {
	d := validSelect(false)
	binary.BigEndian.PutUint16(d[0x10:], 13)
	_, err := ParseSelect(d, soloCtx())
	expectErr(t, err, "invalid spot")
}

func TestSelectPlayerIdToAidMap(t *testing.T) {
	d := validSelect(false)
	d[0x28], d[0x29], d[0x2a] = 3, 3, 3
	_, err := ParseSelect(d, soloCtx())
	expectErr(t, err, "more than 2 players")

	d = validSelect(false)
	d[0x2b] = 12
	_, err = ParseSelect(d, soloCtx())
	expectErr(t, err, "invalid aid")
}

func TestSelectWinningAidMustBeInRoom(t *testing.T) {
	d := validSelect(false)
	d[0x36] = 9
	_, err := ParseSelect(d, soloCtx())
	expectErr(t, err, "winningAid invalid")
}

// ---- RaceMode ----

func vsRaceMode(localCount, slot1, slot2 uint32) []byte {
	d := make([]byte, RaceModeLen)
	w := &bitWriter{data: d}
	w.write(0, 13)
	w.write(0, 13)
	w.write(0, 13)
	w.write(localCount, 2)
	w.write(slot1, 4)
	w.write(1<<16|30<<10|500, 22) // 1:30.500
	w.write(0, 13)
	w.write(slot2, 4)
	w.write(0, 22)
	w.write(0, 13)
	return d
}

type bitWriter struct {
	data []byte
	pos  int
}

func (w *bitWriter) write(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if v>>i&1 == 1 {
			w.data[w.pos/8] |= 1 << (7 - w.pos%8)
		}
		w.pos++
	}
}

func TestRaceModeVSValid(t *testing.T) {
	r, err := ParseRaceMode(vsRaceMode(1, 3, 0), soloCtx(), common.ModeVS, true)
	if err != nil {
		t.Fatal(err)
	}
	ft := r.VS.FinishTimes[0]
	if ft.Minutes != 1 || ft.Seconds != 30 || ft.Milliseconds != 500 {
		t.Fatalf("bad finish time decode: %+v", ft)
	}
}

func TestRaceModeVSSpoofedSlot(t *testing.T) {
	_, err := ParseRaceMode(vsRaceMode(1, 5, 0), soloCtx(), common.ModeVS, true)
	expectErr(t, err, "!= sender aid")
}

func TestRaceModeVSTooManyPlayers(t *testing.T) {
	_, err := ParseRaceMode(vsRaceMode(2, 3, 3), soloCtx(), common.ModeVS, true)
	expectErr(t, err, "local player count")
	if _, err := ParseRaceMode(vsRaceMode(2, 3, 3), guestCtx(), common.ModeVS, true); err != nil {
		t.Fatal(err)
	}
}

func TestRaceModeVSTimers(t *testing.T) {
	d := vsRaceMode(1, 3, 0)
	binary.BigEndian.PutUint16(d[0x22:], maxIdleFrames+1)
	_, err := ParseRaceMode(d, soloCtx(), common.ModeVS, true)
	expectErr(t, err, "idle frames")
}

func TestRaceModeSpectatorSkipsPlayerChecks(t *testing.T) {
	if _, err := ParseRaceMode(vsRaceMode(1, 5, 0), soloCtx(), common.ModeVS, false); err != nil {
		t.Fatal(err)
	}
}

func TestRaceModeBattle(t *testing.T) {
	d := make([]byte, RaceModeLen)
	d[0] = 3<<4 | raceModeNoSlot
	if _, err := ParseRaceMode(d, soloCtx(), common.ModeBalloonBattle, true); err != nil {
		t.Fatal(err)
	}
	d[0] = 3<<4 | 3
	_, err := ParseRaceMode(d, soloCtx(), common.ModeBalloonBattle, true)
	expectErr(t, err, "guest data present")
}

// ---- RaceData ----

func validRaceData() []byte {
	d := make([]byte, RaceDataLen)
	binary.BigEndian.PutUint32(d[0x18:], math.Float32bits(1))
	d[0x25], d[0x26] = 7<<4, 7<<4
	binary.BigEndian.PutUint16(d[0x32:], 4<<7) // rank 4
	return d
}

func TestRaceDataValid(t *testing.T) {
	r, err := ParseRaceData(validRaceData())
	if err != nil {
		t.Fatal(err)
	}
	if r.Rank != 4 || r.StickX != 7 || r.Tilt != 1 {
		t.Fatalf("bad decode: %+v", r)
	}
}

func TestRaceDataInvalid(t *testing.T) {
	d := validRaceData()
	d[0x25] = 0xf << 4
	_, err := ParseRaceData(d)
	expectErr(t, err, "stick X")

	d = validRaceData()
	binary.BigEndian.PutUint16(d[0x32:], 13<<7)
	_, err = ParseRaceData(d)
	expectErr(t, err, "invalid rank")

	d = validRaceData()
	binary.BigEndian.PutUint32(d[0x18:], math.Float32bits(float32(math.NaN())))
	_, err = ParseRaceData(d)
	expectErr(t, err, "invalid tilt")
}

func TestRaceDataCountMustMatchPlayers(t *testing.T) {
	two := append(validRaceData(), validRaceData()...)
	_, err := ParseRaceDataRecords(two, soloCtx())
	expectErr(t, err, "record count")
	if _, err := ParseRaceDataRecords(two, guestCtx()); err != nil {
		t.Fatal(err)
	}
}

// ---- User ----

func friendCode(pid uint32, gameId string) uint64 {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, pid)
	b[4], b[5], b[6], b[7] = gameId[3], gameId[2], gameId[1], gameId[0]
	digest := md5.Sum(b)
	return uint64(pid) | uint64(digest[0]&0xfe)<<31
}

func validUser() []byte {
	d := make([]byte, UserLen)
	binary.BigEndian.PutUint32(d[0x0:], 1)
	binary.BigEndian.PutUint16(d[0x4:], 2)
	binary.BigEndian.PutUint64(d[0xa8:], friendCode(600000123, "RMCE"))
	binary.BigEndian.PutUint16(d[0xb8:], 5000)
	d[0xbc], d[0xbd] = 'E', 1
	return d
}

func TestUserValid(t *testing.T) {
	if _, err := ParseUser(validUser(), soloCtx()); err != nil {
		t.Fatal(err)
	}
}

func TestUserInvalid(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte)
		err    string
	}{
		{"forged friend code", func(d []byte) { d[0xa8+3] ^= 0x10 }, "invalid friend code"},
		{"VR over cap", func(d []byte) { binary.BigEndian.PutUint16(d[0xb8:], 10000) }, "VR"},
		{"guest Mii without guest", func(d []byte) { binary.BigEndian.PutUint32(d[0x0:], 3) }, "guest Mii"},
		{"bad region", func(d []byte) { d[0xbd] = 9 }, "invalid region"},
		{"bad game region", func(d []byte) { d[0xbc] = 'X' }, "game region"},
		{"bad max Mii count", func(d []byte) { binary.BigEndian.PutUint16(d[0x4:], 3) }, "max Mii count"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := validUser()
			c.mutate(d)
			_, err := ParseUser(d, soloCtx())
			expectErr(t, err, c.err)
		})
	}
}

// ---- Item ----

func TestItem(t *testing.T) {
	d := []byte{0, byte(common.NoItem), byte(common.NoItem), 0, 0, 0, 0, 0}
	if _, err := ParseItemRecords(d, soloCtx()); err != nil {
		t.Fatal(err)
	}

	bad := append([]byte{}, d...)
	bad[1] = 0x15
	_, err := ParseItem(bad)
	expectErr(t, err, "held item")

	bad = append([]byte{}, d...)
	bad[7] = 1
	_, err = ParseItem(bad)
	expectErr(t, err, "padding")

	_, err = ParseItemRecords(append(d, d...), soloCtx())
	expectErr(t, err, "record count")
}

// ---- Event ----

func emptyEvent() []byte {
	d := make([]byte, EventSlotCount)
	for i := range d {
		d[i] = byte(common.ObjNone)
	}
	return d
}

func TestEventEmpty(t *testing.T) {
	if _, err := ParseEvent(emptyEvent()); err != nil {
		t.Fatal(err)
	}
}

func TestEventUsed(t *testing.T) {
	d := emptyEvent()
	d[0] = byte(common.EventUsed)<<5 | byte(common.ObjMushroom)
	d = append(d, 0, 0, 0)
	e, err := ParseEvent(d)
	if err != nil {
		t.Fatal(err)
	}
	if e.Slots[0].Action != common.EventUsed || e.Slots[0].Object != common.ObjMushroom {
		t.Fatalf("bad decode: %+v", e.Slots[0])
	}
}

func TestEventInvalid(t *testing.T) {
	d := emptyEvent()
	d[0] = byte(common.EventUsed)<<5 | byte(common.ObjMushroom)
	_, err := ParseEvent(d)
	expectErr(t, err, "too short")

	d = emptyEvent()
	d[1] = byte(common.EventNone)<<5 | byte(common.ObjBanana)
	_, err = ParseEvent(d)
	expectErr(t, err, "action 0")

	d = emptyEvent()
	d[2] = 0x0f
	_, err = ParseEvent(d)
	expectErr(t, err, "invalid object")
}
