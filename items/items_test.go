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
	// 1st always gets 1st place odds and last always gets 12th place odds.
	for count := 2; count <= 12; count++ {
		if c := vs(1, count); c != 0 {
			t.Fatalf("1st of %d -> column %d", count, c)
		}
		if c := vs(count, count); c != 11 {
			t.Fatalf("last of %d -> column %d", count, c)
		}
	}
	for rank := 1; rank <= 12; rank++ {
		if c := vs(rank, 12); c != rank-1 {
			t.Fatalf("12 players: rank %d -> column %d", rank, c)
		}
	}
	// Ranks outside the room are clamped.
	if c := vs(20, 6); c != 11 {
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
	e, err := NewEngine(slot, map[common.ItemType]time.Duration{})
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

func TestCooldownGivesMushroom(t *testing.T) {
	slot := &ItemSlot{}
	for range 12 {
		table := Table{Chances: make([][rows]int, 12)}
		for col := range table.Chances {
			table.Chances[col][common.BlueShell] = 200
		}
		slot.Tables = append(slot.Tables, table)
	}
	e, err := NewEngine(slot, map[common.ItemType]time.Duration{common.BlueShell: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	req := Request{Mode: common.ModeVS, Rank: 12, PlayerCount: 12}
	now := time.Now()
	if item, _ := e.Decide(req, now); item != common.BlueShell {
		t.Fatalf("first roll %v", item)
	}
	if item, _ := e.Decide(req, now.Add(10*time.Second)); item != common.Mushroom {
		t.Fatalf("roll during cooldown %v", item)
	}
	if item, _ := e.Decide(req, now.Add(31*time.Second)); item != common.BlueShell {
		t.Fatalf("roll after cooldown %v", item)
	}
}

func TestNewEngineNeedsOnlineTables(t *testing.T) {
	slot, _ := ParseItemSlot(encode(6, 12, true)) // ItemSlotTable.slt has 6 tables.
	if _, err := NewEngine(slot, nil); err == nil {
		t.Fatal("engine accepted a file without online battle tables")
	}
}
