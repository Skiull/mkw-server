package items

import (
	"testing"
	"time"

	"mkw-server/common"
)

const rows = common.ItemSlotRowCount

// encode builds an ItemSlot.bin with Nintendo-style columns: column c gives item c%rows
// 150 and item (c+1)%rows 50.
func encode(tables int, columns int, itemMajor bool) []byte {
	out := []byte{byte(tables)}
	for range tables {
		if itemMajor {
			out = append(out, byte(rows), byte(columns))
		} else {
			out = append(out, byte(columns), byte(rows))
		}
		cells := make([]byte, rows*columns)
		for col := range columns {
			for item := range rows {
				v := byte(0)
				if item == col%rows {
					v = 150
				} else if item == (col+1)%rows {
					v = 50
				}
				if itemMajor {
					cells[item*columns+col] = v
				} else {
					cells[col*rows+item] = v
				}
			}
		}
		out = append(out, cells...)
	}
	return out
}

func TestParseItemSlotBothOrders(t *testing.T) {
	for _, itemMajor := range []bool{true, false} {
		slot, err := ParseItemSlot(encode(12, 12, itemMajor))
		if err != nil {
			t.Fatal(err)
		}
		if len(slot.Tables) != 12 {
			t.Fatalf("got %d tables", len(slot.Tables))
		}
		col := slot.Tables[TableOnlineVS].Chances[5]
		if col[5] != 150 || col[6] != 50 {
			t.Fatalf("itemMajor=%v: wrong cells %v", itemMajor, col)
		}
	}
}

func TestParseItemSlotErrors(t *testing.T) {
	if _, err := ParseItemSlot(nil); err == nil {
		t.Fatal("empty file accepted")
	}
	data := encode(1, 12, true)
	if _, err := ParseItemSlot(data[:len(data)-1]); err == nil {
		t.Fatal("truncated file accepted")
	}
	if _, err := ParseItemSlot([]byte{1, 5, 5}); err == nil {
		t.Fatal("table without 19 items accepted")
	}
}

func TestColumn(t *testing.T) {
	vs := func(rank, count int) int {
		return Column(Request{Mode: common.ModeVS, Rank: rank, PlayerCount: count}, 12)
	}
	// Smaller rooms use a subset of the 12 player columns (Wexos's tables).
	cases := []struct{ rank, count, want int }{
		{1, 2, 0}, {2, 2, 3},
		{3, 3, 5},
		{4, 4, 8},
		{5, 5, 9},
		{6, 6, 10},
		{7, 7, 11},
		{2, 8, 1}, {8, 8, 11},
		{5, 10, 5},
		{6, 11, 6},
	}
	for _, c := range cases {
		if got := vs(c.rank, c.count); got != c.want {
			t.Fatalf("rank %d of %d -> column %d, want %d", c.rank, c.count, got, c.want)
		}
	}
	for rank := 1; rank <= 12; rank++ {
		if c := vs(rank, 12); c != rank-1 {
			t.Fatalf("12 players: rank %d -> column %d", rank, c)
		}
	}
	// Ranks outside the room are clamped to last place.
	if c := vs(20, 6); c != 10 {
		t.Fatalf("clamp -> %d", c)
	}

	battle := func(behind int) int {
		return Column(Request{Mode: common.ModeBalloonBattle, BehindLeader: behind}, 3)
	}
	if battle(0) != 0 || battle(3) != 1 || battle(5) != 1 || battle(6) != 2 {
		t.Fatal("wrong balloon battle columns")
	}
}

func TestDecideFollowsWeights(t *testing.T) {
	slot, err := ParseItemSlot(encode(12, 12, true))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(slot, &Limits{})
	if err != nil {
		t.Fatal(err)
	}
	e.SeedForTest(1)

	counts := map[common.ItemType]int{}
	const n = 10000
	for range n {
		item, err := e.Decide(Request{Mode: common.ModeVS, Rank: 1, PlayerCount: 12}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		counts[item]++
	}
	// Column 0: 75% Green Shell, 25% Red Shell.
	if len(counts) != 2 || counts[common.GreenShell] < 7000 || counts[common.GreenShell] > 8000 {
		t.Fatalf("unexpected distribution %v", counts)
	}
}

// twoItemSlot makes every column of every table give a or b with equal chance.
func twoItemSlot(a, b common.ItemType) *ItemSlot {
	slot := &ItemSlot{}
	for range 12 {
		table := Table{Chances: make([][rows]int, 12)}
		for col := range table.Chances {
			table.Chances[col][a] = 100
			table.Chances[col][b] = 100
		}
		slot.Tables = append(slot.Tables, table)
	}
	return slot
}

// rollAll rolls n times and reports which items came out.
func rollAll(t *testing.T, e *Engine, req Request, now time.Time, n int) map[common.ItemType]bool {
	t.Helper()
	seen := map[common.ItemType]bool{}
	for range n {
		item, err := e.Decide(req, now)
		if err != nil {
			t.Fatal(err)
		}
		seen[item] = true
	}
	return seen
}

func TestFirstAvailable(t *testing.T) {
	e, err := NewEngine(twoItemSlot(common.Lightning, common.Star), &Limits{
		FirstAvailable: map[common.ItemType]time.Duration{common.Lightning: 30 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Mode: common.ModeVS, Rank: 12, PlayerCount: 12, RaceTime: 25 * time.Second}
	if seen := rollAll(t, e, req, time.Now(), 50); seen[common.Lightning] || !seen[common.Star] {
		t.Fatalf("at 25s got %v, want only Star (rerolled, not Mushroom)", seen)
	}
	req.RaceTime = 31 * time.Second
	if seen := rollAll(t, e, req, time.Now(), 50); !seen[common.Lightning] {
		t.Fatalf("at 31s never got Lightning: %v", seen)
	}
}

func TestCooldown(t *testing.T) {
	e, err := NewEngine(twoItemSlot(common.Blooper, common.Star), &Limits{
		Cooldown: map[common.ItemType]time.Duration{common.Blooper: 15 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Mode: common.ModeVS, Rank: 12, PlayerCount: 12, RaceTime: time.Minute}
	now := time.Now()
	for range 100 {
		if item, _ := e.Decide(req, now); item == common.Blooper {
			break
		}
	}
	if seen := rollAll(t, e, req, now.Add(10*time.Second), 50); seen[common.Blooper] {
		t.Fatal("Blooper pulled during its cooldown")
	}
	if seen := rollAll(t, e, req, now.Add(16*time.Second), 50); !seen[common.Blooper] {
		t.Fatal("Blooper never pulled after its cooldown")
	}
}

func TestMaxInPlay(t *testing.T) {
	e, err := NewEngine(twoItemSlot(common.ThunderCloud, common.Star), &Limits{
		MaxInPlay: map[common.ItemType]int{common.ThunderCloud: 1, common.Star: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Mode: common.ModeVS, Rank: 12, PlayerCount: 12, RaceTime: time.Minute,
		InPlay: map[common.ItemObject]int{common.ObjThunderCloud: 1}}
	if seen := rollAll(t, e, req, time.Now(), 50); seen[common.ThunderCloud] {
		t.Fatal("second Thunder Cloud pulled")
	}
	// Nothing available: Mushroom.
	req.InPlay[common.ObjStar] = 3
	if seen := rollAll(t, e, req, time.Now(), 20); len(seen) != 1 || !seen[common.Mushroom] {
		t.Fatalf("with nothing available got %v, want Mushroom", seen)
	}
	// Thunder Cloud is available again right after it's gone: no timer.
	req.InPlay = nil
	if seen := rollAll(t, e, req, time.Now(), 50); !seen[common.ThunderCloud] {
		t.Fatal("Thunder Cloud never pulled once none were in play")
	}
}

func TestNewEngineNeedsOnlineTables(t *testing.T) {
	slot, _ := ParseItemSlot(encode(6, 12, true)) // ItemSlotTable.slt has 6 tables.
	if _, err := NewEngine(slot, nil); err == nil {
		t.Fatal("engine accepted a file without online battle tables")
	}
}

func TestNoThunderCloudWhileInvincible(t *testing.T) {
	e := &Engine{limits: Limits{}, lastGranted: map[common.ItemType]time.Time{}}
	req := Request{Mode: common.ModeVS, RaceTime: time.Minute}
	if !e.Available(common.ThunderCloud, req, time.Now()) {
		t.Fatal("Thunder Cloud unavailable normally")
	}
	req.Invincible = true
	if e.Available(common.ThunderCloud, req, time.Now()) {
		t.Fatal("Thunder Cloud available in a Star/Mega/Bullet")
	}
	if !e.Available(common.Mushroom, req, time.Now()) {
		t.Fatal("other items blocked while invincible")
	}
}
