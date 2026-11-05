package items

import "encoding/binary"

// Leaf piles (GOBJ 0x176 karehayama, Maple Treeway) can drop an item when a player drives
// through them: a Mushroom, a Banana or sometimes a Star, straight from the course rather
// than from an item box. Settings: 2 = item to give, 3 = chance of giving it.
const leafPileObject = 0x176

type LeafPile struct {
	Pos    [3]float32
	Item   uint16
	Chance uint16
}

func kmpLeafPiles(kmp []byte) ([]LeafPile, error) {
	data, count, err := kmpSection(kmp, kmpGOBJOffset, "GOBJ", gobjEntryLen)
	if err != nil {
		return nil, err
	}
	var piles []LeafPile
	for i := range count {
		e := data[i*gobjEntryLen:]
		if binary.BigEndian.Uint16(e) != leafPileObject {
			continue
		}
		settings := e[0x2a:]
		piles = append(piles, LeafPile{
			Pos:    vec3(e[4:]),
			Item:   binary.BigEndian.Uint16(settings[2:]),
			Chance: binary.BigEndian.Uint16(settings[4:]),
		})
	}
	return piles, nil
}
