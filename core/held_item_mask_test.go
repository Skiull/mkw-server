package core

import (
	"testing"

	"mkw-server/common"
	"mkw-server/core/records"
)

func TestMaskHeldItems(t *testing.T) {
	// Packet bytes before the ITEM records don't matter to the mask.
	const off = 4
	cases := []struct {
		name               string
		held, trail, phase byte
		wantHeld, wantPh   byte
	}{
		{"decided red shell", byte(common.RedShell), byte(common.NoItem), heldPhaseDecided, byte(common.NoItem), heldPhaseNoItem},
		{"triple bananas, two left", byte(common.TripleBananas), byte(common.Banana), heldPhaseTwoLeft, byte(common.NoItem), heldPhaseNoItem},
		{"thunder cloud kept", byte(common.ThunderCloud), byte(common.NoItem), heldPhaseFinalised, byte(common.ThunderCloud), heldPhaseFinalised},
		{"empty slot", byte(common.NoItem), byte(common.Banana), heldPhaseNoItem, byte(common.NoItem), heldPhaseNoItem},
	}
	for _, c := range cases {
		data := make([]byte, off+records.ItemLen)
		rec := data[off:]
		rec[0], rec[1], rec[2], rec[3] = 9, c.held, c.trail, c.phase
		st, err := records.ParseItem(rec)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		maskHeldItems(data, &RacePacket{Items: []*records.ItemState{st}, itemOffset: off})
		if rec[1] != c.wantHeld || rec[3] != c.wantPh {
			t.Errorf("%s: held %d phase %d, want %d %d", c.name, rec[1], rec[3], c.wantHeld, c.wantPh)
		}
		if rec[0] != 9 || rec[2] != c.trail {
			t.Errorf("%s: timer or trailing item changed", c.name)
		}
		if _, err := records.ParseItem(rec); err != nil {
			t.Errorf("%s: masked record no longer valid: %v", c.name, err)
		}
	}
}
