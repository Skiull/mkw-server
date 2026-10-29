package items

import (
	"os"
	"path/filepath"
	"testing"

	"mkw-server/common"
)

func TestMapleTreewayLeafPiles(t *testing.T) {
	dir := os.Getenv("MKW_COURSE_DIR")
	if dir == "" {
		t.Skip("MKW_COURSE_DIR not set")
	}
	raw, err := os.ReadFile(filepath.Join(dir, courseFiles[common.MapleTreeway]+".szs"))
	if err != nil {
		t.Skip(err)
	}
	c, err := parseCourse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.LeafPiles) != 17 {
		t.Fatalf("got %d leaf piles, want 17", len(c.LeafPiles))
	}
	for _, p := range c.LeafPiles {
		if p.Chance == 0 {
			t.Errorf("pile at %v has no drop chance", p.Pos)
		}
	}
}
