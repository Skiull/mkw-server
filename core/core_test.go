package core

import (
	"crypto/md5"
	"encoding/binary"
	"hash/crc32"
	"math"
	"net"
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
)

// ---- test room ----

func setupRoom(t *testing.T, config Config) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	room = &Room{conn: conn, addr: conn.LocalAddr().(*net.UDPAddr), config: config}
	aids = make(map[string]*Aid)
	kickHandler = nil
}

func addTestAid(t *testing.T, aid byte, hasGuest bool) *Aid {
	t.Helper()
	addr := (&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000 + int(aid)}).String()
	if err := AddAidToRoom(addr, aid, aid == 0, hasGuest); err != nil {
		t.Fatal(err)
	}
	return aids[addr]
}

func handle(a *Aid, data []byte, now time.Time) []net.Addr {
	mu.Lock()
	defer mu.Unlock()
	return handlePacket(a.addr, data, now)
}

// ---- packet builder ----

type packetBuilder struct {
	sender byte
	dests  uint16
	recs   [8][]byte // indexed like the Header's length fields
}

const (
	recRaceInfo = iota + 1
	recRaceMode
	recRoomSelect
	recRaceData
	recUser
	recItem
	recEvent
)

func (b *packetBuilder) build() []byte {
	data := make([]byte, records.HeaderLen)
	data[0] = RacePacketMagic
	data[1] = b.sender
	binary.BigEndian.PutUint16(data[2:4], b.dests)
	data[8] = records.HeaderLen
	for i := 1; i < len(b.recs); i++ {
		data[8+i] = byte(len(b.recs[i]))
		data = append(data, b.recs[i]...)
	}
	binary.BigEndian.PutUint32(data[4:8], crc32.ChecksumIEEE(data))
	return data
}

func raceInfo(course common.Course, countdown uint32) []byte {
	d := make([]byte, records.RaceInfoLen)
	binary.BigEndian.PutUint32(d[0:], countdown)
	d[0xe], d[0xf] = byte(common.StandardKartM), byte(common.Mario)
	d[0x10], d[0x11] = byte(common.RaceInfoDefaultVehicle), byte(common.RaceInfoDefaultCharacter)
	d[0x16] = byte(course)
	for i := range 12 {
		d[0x18+i] = records.NoAid
	}
	d[0x24] = byte(common.CC150)
	return d
}

func raceData(rank byte) []byte {
	d := make([]byte, records.RaceDataLen)
	binary.BigEndian.PutUint32(d[0x18:], math.Float32bits(1))
	d[0x25], d[0x26] = 7<<4, 7<<4
	binary.BigEndian.PutUint16(d[0x32:], uint16(rank)<<7)
	return d
}

func itemRec(held, trailing common.ItemType) []byte {
	return []byte{0, byte(held), byte(trailing), 0, 0, 0, 0, 0}
}

func eventRec(slots ...byte) []byte {
	d := make([]byte, records.EventSlotCount)
	for i := range d {
		d[i] = byte(common.ObjNone)
	}
	copy(d, slots)
	return append(d, make([]byte, 3*len(slots))...)
}

func userRec(pid uint32) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, pid)
	copy(b[4:], "ECMR")
	fc := uint64(pid) | uint64(md5.Sum(b)[0]&0xfe)<<31

	d := make([]byte, records.UserLen)
	binary.BigEndian.PutUint32(d[0x0:], 1)
	binary.BigEndian.PutUint16(d[0x4:], 2)
	binary.BigEndian.PutUint64(d[0xa8:], fc)
	d[0xbc], d[0xbd] = 'E', 1
	return d
}

func racingPacket(sender byte, dests uint16, rank byte) *packetBuilder {
	b := &packetBuilder{sender: sender, dests: dests}
	b.recs[recRaceInfo] = raceInfo(common.LuigiCircuit, 100)
	b.recs[recRaceData] = raceData(rank)
	return b
}

// ---- tests ----

func TestRelayToDestinations(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0, a1, a2 := addTestAid(t, 0, false), addTestAid(t, 1, false), addTestAid(t, 2, false)

	to := handle(a0, racingPacket(0, 1<<1|1<<2, 1).build(), time.Now())
	if len(to) != 2 {
		t.Fatalf("expected relay to 2 aids, got %v", to)
	}
	for _, addr := range to {
		if addr != a1.addr && addr != a2.addr {
			t.Fatalf("relayed to unexpected address %v", addr)
		}
	}
}

func TestEmptyAndJunkPacketsDontCrash(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	for _, data := range [][]byte{{}, {RacePacketMagic}, {RacePacketMagic, 0, 0, 0, 1}, []byte("IR")} {
		if to := handle(a0, data, time.Now()); to != nil {
			t.Fatalf("junk packet %v was relayed", data)
		}
	}
}

func TestSpoofedSenderAidDropped(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	addTestAid(t, 1, false)

	if to := handle(a0, racingPacket(1, 1<<0, 1).build(), time.Now()); to != nil {
		t.Fatal("packet claiming another aid was relayed")
	}
	if len(a0.strikes) != 1 {
		t.Fatalf("expected a strike, got %d", len(a0.strikes))
	}
}

func TestDestinationIncludingSelfDropped(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	if to := handle(a0, racingPacket(0, 1<<0, 1).build(), time.Now()); to != nil {
		t.Fatal("packet addressed to its sender was relayed")
	}
}

func TestBadChecksumDropped(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	addTestAid(t, 1, false)
	data := racingPacket(0, 1<<1, 1).build()
	data[len(data)-1] ^= 0xff
	if to := handle(a0, data, time.Now()); to != nil {
		t.Fatal("corrupted packet was relayed")
	}
}

func TestGuestRecordsRequireGuest(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	addTestAid(t, 1, false)
	b := racingPacket(0, 1<<1, 1)
	b.recs[recRaceData] = append(raceData(1), raceData(2)...)
	if to := handle(a0, b.build(), time.Now()); to != nil {
		t.Fatal("two RaceData records from a console without a guest were relayed")
	}
}

func TestKickAfterThreshold(t *testing.T) {
	config := DefaultConfig()
	config.KickThreshold = 3
	setupRoom(t, config)
	a0 := addTestAid(t, 0, false)
	a1 := addTestAid(t, 1, false)

	kicked := make(chan byte, 1)
	SetKickHandler(func(_ *net.UDPAddr, aid byte, reason KickReason) {
		if reason == KickInvalidPackets {
			kicked <- aid
		}
	})

	now := time.Now()
	bad := racingPacket(1, 1<<1, 1).build() // claims aid 1
	for i := range 3 {
		handle(a0, bad, now.Add(time.Duration(i)*time.Millisecond))
	}

	select {
	case aid := <-kicked:
		if aid != 0 {
			t.Fatalf("kicked aid %d, want 0", aid)
		}
	case <-time.After(time.Second):
		t.Fatal("kick handler not called")
	}

	// A kicked aid's valid packets are dropped, and nobody relays to it.
	if to := handle(a0, racingPacket(0, 1<<1, 1).build(), now); to != nil {
		t.Fatal("kicked aid's packet was relayed")
	}
	if to := handle(a1, racingPacket(1, 1<<0, 1).build(), now); len(to) != 0 {
		t.Fatal("packet was relayed to a kicked aid")
	}
}

func TestStrikesExpire(t *testing.T) {
	config := DefaultConfig()
	config.KickThreshold = 3
	config.KickWindow = time.Second
	setupRoom(t, config)
	a0 := addTestAid(t, 0, false)
	addTestAid(t, 1, false)

	now := time.Now()
	bad := racingPacket(1, 1<<1, 1).build()
	for i := range 5 {
		handle(a0, bad, now.Add(time.Duration(i)*2*time.Second))
	}
	if a0.kicked {
		t.Fatal("aid kicked for strikes spread outside the window")
	}
}

func TestKickRequestedByWFC(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	if err := KickAid(a0.addr.String(), KickRequested); err != nil {
		t.Fatal(err)
	}
	if !a0.kicked {
		t.Fatal("aid not kicked")
	}
}

func TestFriendCodePinned(t *testing.T) {
	setupRoom(t, DefaultConfig())
	a0 := addTestAid(t, 0, false)
	addTestAid(t, 1, false)

	b := racingPacket(0, 1<<1, 1)
	b.recs[recUser] = userRec(600000123)
	if to := handle(a0, b.build(), time.Now()); len(to) != 1 {
		t.Fatal("first User record rejected")
	}

	b.recs[recUser] = userRec(600000999)
	if to := handle(a0, b.build(), time.Now()); to != nil {
		t.Fatal("changed friend code was accepted")
	}
}

func TestDuplicateAidRejected(t *testing.T) {
	setupRoom(t, DefaultConfig())
	addTestAid(t, 0, false)
	if err := AddAidToRoom("127.0.0.1:50000", 0, false, false); err == nil {
		t.Fatal("second aid with the same number was added")
	}
	if err := AddAidToRoom("127.0.0.1:50001", 12, false, false); err == nil {
		t.Fatal("aid 12 was added")
	}
}

// ---- server items ----

// testItemSlot makes every column of every table always give one item.
func testItemSlot(item common.ItemType) *items.ItemSlot {
	slot := &items.ItemSlot{}
	for range 12 {
		table := items.Table{Chances: make([][common.ItemSlotRowCount]int, 12)}
		for col := range table.Chances {
			table.Chances[col][item] = 200
		}
		slot.Tables = append(slot.Tables, table)
	}
	return slot
}

func itemRoom(t *testing.T, item common.ItemType, enforce bool) (*Aid, *Aid) {
	t.Helper()
	engine, err := items.NewEngine(testItemSlot(item), map[common.ItemType]time.Duration{})
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.ItemEngine = engine
	config.EnforceItems = enforce
	setupRoom(t, config)

	a0, a1 := addTestAid(t, 0, false), addTestAid(t, 1, false)
	a0.isRacer, a1.isRacer = true, true
	return a0, a1
}

func requestItem(t *testing.T, a *Aid, seq byte, now time.Time) common.ItemType {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	handleItemRequest(a, []byte{'I', 'R', 0, seq}, now)
	return a.players[0].lastRequestItem
}

func TestItemGrantAndEnforce(t *testing.T) {
	a0, _ := itemRoom(t, common.Banana, true)
	now := time.Now()

	// Before the race, requests are refused.
	if item := requestItem(t, a0, 1, now); item != common.NoItem {
		t.Fatalf("got %v before the race started", item)
	}

	handle(a0, racingPacket(0, 1<<1, 2).build(), now)
	if item := requestItem(t, a0, 2, now); item != common.Banana {
		t.Fatalf("got %v, want Banana", item)
	}

	// Retransmission gets the same answer; a new request while holding is refused.
	if item := requestItem(t, a0, 2, now); item != common.Banana {
		t.Fatalf("retransmit got %v", item)
	}
	if item := requestItem(t, a0, 3, now.Add(2*time.Second)); item != common.NoItem {
		t.Fatalf("got %v while still holding an item", item)
	}

	// Holding the granted item is fine; holding anything else isn't.
	ok := racingPacket(0, 1<<1, 2)
	ok.recs[recItem] = itemRec(common.Banana, common.NoItem)
	if to := handle(a0, ok.build(), now); len(to) != 1 {
		t.Fatal("packet holding the granted item was dropped")
	}

	hacked := racingPacket(0, 1<<1, 2)
	hacked.recs[recItem] = itemRec(common.BlueShell, common.NoItem)
	if to := handle(a0, hacked.build(), now); to != nil {
		t.Fatal("packet holding an ungranted Blue Shell was relayed")
	}

	// Shooting the granted item is fine; shooting something else isn't.
	shot := racingPacket(0, 1<<1, 2)
	shot.recs[recItem] = itemRec(common.NoItem, common.Banana)
	shot.recs[recEvent] = eventRec(byte(common.EventUsed)<<5 | byte(common.ObjBanana))
	if to := handle(a0, shot.build(), now); len(to) != 1 {
		t.Fatal("packet using the granted item was dropped")
	}

	spam := racingPacket(0, 1<<1, 2)
	spam.recs[recItem] = itemRec(common.NoItem, common.NoItem)
	spam.recs[recEvent] = eventRec(
		byte(common.EventUsed)<<5|byte(common.ObjBanana),
		byte(common.EventUsed)<<5|byte(common.ObjBlueShell),
	)
	if to := handle(a0, spam.build(), now); to != nil {
		t.Fatal("packet using an ungranted Blue Shell was relayed")
	}

	// Once the item is used, the player can get another.
	if item := requestItem(t, a0, 4, now.Add(3*time.Second)); item != common.Banana {
		t.Fatalf("got %v after using the previous item", item)
	}
}

func TestItemViolationsLoggedWhenNotEnforced(t *testing.T) {
	a0, _ := itemRoom(t, common.Banana, false)
	b := racingPacket(0, 1<<1, 2)
	b.recs[recItem] = itemRec(common.BlueShell, common.NoItem)
	if to := handle(a0, b.build(), time.Now()); len(to) != 1 {
		t.Fatal("packet dropped although enforcement is off")
	}
}

func TestItemRequestForMissingGuest(t *testing.T) {
	a0, _ := itemRoom(t, common.Banana, true)
	mu.Lock()
	handleItemRequest(a0, []byte{'I', 'R', 1, 1}, time.Now())
	mu.Unlock()
	if len(a0.strikes) != 1 {
		t.Fatal("request for a guest the aid doesn't have wasn't struck")
	}
}
