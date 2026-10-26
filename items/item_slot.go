package items

import (
	"errors"
	"fmt"
	"os"

	"mkw-server/common"
)

// Table indices in ItemSlot.bin that online play uses.
const (
	TableOnlineVS            = 4
	TableOnlineBalloonBattle = 10
	TableOnlineCoinRunners   = 11
)

// Sum of a column's chances in Nintendo's tables. Each unit is 0.5%.
const nintendoColumnSum = 200

// Table is one item probability table. Chances[col][item] is the weight of an item for a
// column (a race position, or how far behind the leader a battle player is).
type Table struct {
	Chances [][common.ItemSlotRowCount]int
}

func (t *Table) Columns() int {
	return len(t.Chances)
}

// ItemSlot is a parsed ItemSlot.bin (from /Race/Common.szs).
type ItemSlot struct {
	Tables []Table
}

func LoadItemSlot(path string) (*ItemSlot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseItemSlot(data)
}

// ParseItemSlot parses ItemSlot.bin: a table count, then per table two dimension bytes
// followed by the cells. One dimension is always the 19 items. Sources disagree on which
// byte is which and on the cell order, so both orders are tried and the one where every
// column sums to 200 (true for Nintendo's files) wins. Edited files fall back to
// item-major order.
func ParseItemSlot(data []byte) (*ItemSlot, error) {
	if len(data) < 1 {
		return nil, errors.New("empty file")
	}

	tableCount := int(data[0])
	offset := 1
	slot := &ItemSlot{}

	for i := range tableCount {
		if offset+2 > len(data) {
			return nil, fmt.Errorf("table %d: truncated header", i)
		}
		a, b := int(data[offset]), int(data[offset+1])
		offset += 2

		size := a * b
		if offset+size > len(data) {
			return nil, fmt.Errorf("table %d: truncated cells", i)
		}
		cells := data[offset : offset+size]
		offset += size

		var columns int
		switch common.ItemSlotRowCount {
		case a:
			columns = b
		case b:
			columns = a
		default:
			return nil, fmt.Errorf("table %d: no dimension is %d items (%dx%d)", i, common.ItemSlotRowCount, a, b)
		}
		if columns == 0 {
			return nil, fmt.Errorf("table %d: no columns", i)
		}

		itemMajor := buildTable(cells, columns, true)
		columnMajor := buildTable(cells, columns, false)

		table := itemMajor
		if !itemMajor.hasNintendoSums() && columnMajor.hasNintendoSums() {
			table = columnMajor
		}
		slot.Tables = append(slot.Tables, table)
	}

	return slot, nil
}

func buildTable(cells []byte, columns int, itemMajor bool) Table {
	t := Table{Chances: make([][common.ItemSlotRowCount]int, columns)}
	for col := range columns {
		for item := range common.ItemSlotRowCount {
			var idx int
			if itemMajor {
				idx = item*columns + col
			} else {
				idx = col*common.ItemSlotRowCount + item
			}
			t.Chances[col][item] = int(cells[idx])
		}
	}
	return t
}

func (t *Table) hasNintendoSums() bool {
	for _, col := range t.Chances {
		sum := 0
		for _, c := range col {
			sum += c
		}
		if sum != nintendoColumnSum {
			return false
		}
	}
	return true
}
