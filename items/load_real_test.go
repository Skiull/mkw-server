package items

import (
	"os"
	"testing"

	"mkw-server/common"
)

// Loads a real Common.szs when MKW_COMMON_SZS points at one. Skipped otherwise, since the
// game's files aren't in the repo.
func TestLoadRealCommonSZS(t *testing.T) {
	path := os.Getenv("MKW_COMMON_SZS")
	if path == "" {
		t.Skip("MKW_COMMON_SZS not set")
	}
	slot, err := LoadItemSlot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(slot.Tables) != 12 {
		t.Fatalf("got %d tables, want 12", len(slot.Tables))
	}
	vs := slot.Tables[TableOnlineVS]
	if vs.Columns() != 12 || !vs.hasNintendoSums() {
		t.Fatalf("online VS table looks wrong: %d columns", vs.Columns())
	}
	first, last := vs.Chances[0], vs.Chances[11]
	t.Logf("1st place: %v", first)
	t.Logf("12th place: %v", last)
	if last[common.BlueShell] != 0 && first[common.BlueShell] != 0 {
		t.Log("note: both 1st and 12th can get Blue Shells")
	}
	if _, err := NewEngine(slot, nil); err != nil {
		t.Fatal(err)
	}
}
