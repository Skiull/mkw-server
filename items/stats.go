package items

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"mkw-server/common"
)

// Stats are the per-vehicle and per-character speed multipliers for each KCL floor type,
// from kartParam.bin and driverParam.bin in the game's /Race/Common.szs
// (https://mkwiiki.org/wiki/KartParam.bin). A combo's multiplier on a floor is the
// vehicle's plus the character's bonus: offroad ranges from about 0.55 to 0.91 of full
// speed depending on the combo.
type Stats struct {
	kart   [][32]float32 // By vehicle ID.
	driver [][32]float32 // By driverParam entry: 24 characters, then Mii small/medium/large.
	// By vehicle ID: Mega Mushroom scale (multiplies the 120 u/f cap, 1.55-2.70) and
	// shocked scale (size when shrunk, 0.40-0.70).
	megaScale, shockScale []float32
}

const (
	paramEntryLen   = 0x18c
	paramKCLSpeed   = 0x70
	paramMegaScale  = 0x184
	paramShockScale = 0x188
	driverMiiEntry  = 24 // Mii entries in driverParam.bin start here.
	driverMiiCount  = 3
	numKCLFloorType = 32
)

// LoadStats reads the vehicle and character stats from Common.szs.
func LoadStats(path string) (*Stats, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) >= 4 && string(data[:4]) == yaz0Magic {
		if data, err = decompressYaz0(data); err != nil {
			return nil, err
		}
	}
	s := &Stats{}
	if s.kart, err = readParam(data, "kartParam.bin"); err != nil {
		return nil, err
	}
	if s.megaScale, err = readParamFloat(data, "kartParam.bin", paramMegaScale); err != nil {
		return nil, err
	}
	if s.shockScale, err = readParamFloat(data, "kartParam.bin", paramShockScale); err != nil {
		return nil, err
	}
	if s.driver, err = readParam(data, "driverParam.bin"); err != nil {
		return nil, err
	}
	if len(s.kart) < 36 || len(s.driver) < driverMiiEntry+driverMiiCount {
		return nil, fmt.Errorf("unexpected stat counts (%d vehicles, %d characters)", len(s.kart), len(s.driver))
	}
	return s, nil
}

func readParam(arc []byte, name string) ([][32]float32, error) {
	d, err := findInU8(arc, name)
	if err != nil {
		return nil, err
	}
	if len(d) < 4 {
		return nil, fmt.Errorf("%s is too small", name)
	}
	n := int(binary.BigEndian.Uint32(d))
	if 4+n*paramEntryLen > len(d) {
		return nil, fmt.Errorf("%s is truncated", name)
	}
	out := make([][32]float32, n)
	for i := range out {
		e := d[4+i*paramEntryLen+paramKCLSpeed:]
		for k := range numKCLFloorType {
			out[i][k] = math.Float32frombits(binary.BigEndian.Uint32(e[k*4:]))
		}
	}
	return out, nil
}

// FloorSpeed returns how much of its speed a combo keeps on a KCL floor type. An unknown
// vehicle or character gets the most lenient value of any.
func (s *Stats) FloorSpeed(v common.Vehicle, c common.Character, floor uint8) float32 {
	if s == nil || int(floor) >= numKCLFloorType {
		return 1
	}
	var kart float32
	if int(v) < len(s.kart) {
		kart = s.kart[v][floor]
	} else {
		for _, k := range s.kart {
			kart = max(kart, k[floor])
		}
	}
	var driver float32
	switch {
	case int(c) < driverMiiEntry:
		driver = s.driver[c][floor]
	default:
		// Miis (by size) and anything unknown: the best Mii or character bonus.
		for _, d := range s.driver {
			driver = max(driver, d[floor])
		}
	}
	return kart + driver
}

// readParamFloat reads one float field from every entry of a param file.
func readParamFloat(arc []byte, name string, off int) ([]float32, error) {
	d, err := findInU8(arc, name)
	if err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(d))
	if 4+n*paramEntryLen > len(d) {
		return nil, fmt.Errorf("%s is truncated", name)
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.BigEndian.Uint32(d[4+i*paramEntryLen+off:]))
	}
	return out, nil
}

// MegaScale is how much a Mega Mushroom grows the vehicle, which multiplies its speed cap.
// An unknown vehicle gets the largest.
func (s *Stats) MegaScale(v common.Vehicle) float32 {
	return pick(s, v, func(s *Stats) []float32 { return s.megaScale }, maxOf)
}

// ShockScale is the vehicle's size while shrunk. An unknown vehicle gets the largest.
func (s *Stats) ShockScale(v common.Vehicle) float32 {
	return pick(s, v, func(s *Stats) []float32 { return s.shockScale }, maxOf)
}

func maxOf(vs []float32) float32 {
	var m float32
	for _, v := range vs {
		m = max(m, v)
	}
	return m
}

func pick(s *Stats, v common.Vehicle, field func(*Stats) []float32, fallback func([]float32) float32) float32 {
	if s == nil {
		return 0
	}
	vs := field(s)
	if int(v) < len(vs) {
		return vs[v]
	}
	return fallback(vs)
}
