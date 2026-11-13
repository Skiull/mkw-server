package items

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"mkw-server/common"
)

// Course is what the server knows about a course's layout, read from the game's own files.
// Formats from the community's docs: https://mkwiiki.org/wiki/KMP_(File_Format) and
// https://mkwiiki.org/wiki/KCL_(File_Format).
type Course struct {
	Boxes     []Box
	LeafPiles []LeafPile // Leaf piles that can drop items (Maple Treeway).
	// Conveyor belt objects (Toad's Factory): items on them move.
	Belts       [][3]float32
	Hazards     []Hazard     // Objects that destroy items lying on the track (hazards.go).
	Checkpoints *Checkpoints // Nil for battle arenas (no checkpoints).
	Respawns    [][3]float32 // JGPT respawn points, by index.
	// Item route segments that end at a DONT_DROP item point: a Bullet Bill can't end on
	// them (pits, cannons). See item_points.go.
	NoDropSegments [][2][3]float32
	Walls       *Walls       // Nil if course.kcl couldn't be read.
	Floors      *Floors      // Nil if course.kcl couldn't be read.
}

// KMP header fields holding section offsets (relative to the end of the header).
const (
	kmpITPTOffset = 0x1c
	kmpITPHOffset = 0x20
	kmpCKPTOffset = 0x24
	kmpCKPHOffset = 0x28
	kmpJGPTOffset = 0x3c
)

// LoadCourses reads every course from a folder holding the game's /Race/Course .szs files.
// Missing or unreadable courses are reported and skipped.
func LoadCourses(dir string) (map[common.Course]*Course, []error) {
	courses := make(map[common.Course]*Course)
	var errs []error
	for id, name := range courseFiles {
		raw, err := os.ReadFile(filepath.Join(dir, name+".szs"))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c, err := parseCourse(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		courses[id] = c
	}
	return courses, errs
}

func parseCourse(szs []byte) (*Course, error) {
	arc := szs
	if len(szs) >= 4 && string(szs[:4]) == "Yaz0" {
		var err error
		if arc, err = decompressYaz0(szs); err != nil {
			return nil, err
		}
	}
	kmp, err := findInU8(arc, "course.kmp")
	if err != nil {
		return nil, err
	}

	c := &Course{}
	if c.Boxes, err = kmpBoxes(kmp); err != nil {
		return nil, err
	}
	if c.LeafPiles, err = kmpLeafPiles(kmp); err != nil {
		return nil, err
	}
	if c.Belts, err = kmpBelts(kmp); err != nil {
		return nil, err
	}
	if c.Hazards, err = kmpHazards(kmp); err != nil {
		return nil, err
	}
	if c.Respawns, err = kmpRespawns(kmp); err != nil {
		return nil, err
	}
	if c.Checkpoints, err = kmpCheckpoints(kmp); err != nil {
		return nil, err
	}
	if c.NoDropSegments, err = kmpNoDropSegments(kmp); err != nil {
		return nil, err
	}
	if kcl, err := findInU8(arc, "course.kcl"); err == nil {
		if c.Walls, c.Floors, err = parseKCL(kcl); err != nil {
			return nil, fmt.Errorf("course.kcl: %w", err)
		}
	}
	return c, nil
}

// kmpSection returns a section's entries and count, checking its name and size.
func kmpSection(kmp []byte, headerField int, name string, entryLen int) ([]byte, int, error) {
	if len(kmp) < 0x4c || string(kmp[:4]) != kmpMagic {
		return nil, 0, errors.New("not a KMP file")
	}
	headerLen := int(binary.BigEndian.Uint16(kmp[0xa:]))
	off := headerLen + int(binary.BigEndian.Uint32(kmp[headerField:]))
	if off < 0 || off+8 > len(kmp) || string(kmp[off:off+4]) != name {
		return nil, 0, fmt.Errorf("%s section not found", name)
	}
	count := int(binary.BigEndian.Uint16(kmp[off+4:]))
	if off+8+count*entryLen > len(kmp) {
		return nil, 0, fmt.Errorf("%s section is truncated", name)
	}
	return kmp[off+8:], count, nil
}

func f32(b []byte) float32 {
	return math.Float32frombits(binary.BigEndian.Uint32(b))
}

// kmpRespawns reads the JGPT respawn points (0x1c bytes each: position, rotation, id, extra).
func kmpRespawns(kmp []byte) ([][3]float32, error) {
	data, count, err := kmpSection(kmp, kmpJGPTOffset, "JGPT", 0x1c)
	if err != nil {
		return nil, err
	}
	points := make([][3]float32, count)
	for i := range points {
		e := data[i*0x1c:]
		points[i] = [3]float32{f32(e), f32(e[4:]), f32(e[8:])}
	}
	return points, nil
}
