package items

import "testing"

func TestWallsCrosses(t *testing.T) {
	w := &Walls{cells: map[[2]int32][]int32{}}
	// A wall in the plane x=1000, from z=-500 to 500, y=0 to 1000 (two triangles).
	w.add([3][3]float32{{1000, 0, -500}, {1000, 1000, -500}, {1000, 0, 500}})
	w.add([3][3]float32{{1000, 1000, -500}, {1000, 1000, 500}, {1000, 0, 500}})

	if _, hit := w.Crosses([3]float32{900, 100, 0}, [3]float32{1100, 100, 0}); !hit {
		t.Fatal("driving through the wall wasn't caught")
	}
	if _, hit := w.Crosses([3]float32{800, 100, 0}, [3]float32{990, 100, 0}); hit {
		t.Fatal("stopping short of the wall was flagged")
	}
	if _, hit := w.Crosses([3]float32{900, 1500, 0}, [3]float32{1100, 1500, 0}); hit {
		t.Fatal("jumping over the wall was flagged")
	}
	if _, hit := w.Crosses([3]float32{900, 100, 800}, [3]float32{1100, 100, 800}); hit {
		t.Fatal("driving around the wall was flagged")
	}
	var none *Walls
	if _, hit := none.Crosses([3]float32{}, [3]float32{1, 1, 1}); hit {
		t.Fatal("nil walls flagged a crossing")
	}
}

func TestWallsOneWay(t *testing.T) {
	w := &Walls{cells: make(map[[2]int32][]int32)}
	// A ceiling facing down at y=100 (Bowser's Castle has these): blocks from below only.
	w.add([3][3]float32{{-500, 100, -500}, {500, 100, -500}, {0, 100, 500}})
	w.Flags = append(w.Flags, 0x0c)
	w.Norms = append(w.Norms, [3]float32{0, -1, 0})
	if _, hit := w.Crosses([3]float32{0, 200, 0}, [3]float32{0, 0, 0}); hit {
		t.Fatal("falling through a ceiling from above counted as a wall")
	}
	if _, hit := w.Crosses([3]float32{0, 0, 0}, [3]float32{0, 200, 0}); !hit {
		t.Fatal("going up through a ceiling wasn't caught")
	}
}
