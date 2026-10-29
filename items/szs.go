package items

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Readers for the game's archive formats, so --item-slot can point straight at
// /Race/Common.szs from the disc instead of an extracted ItemSlot.bin.

const yaz0Magic = "Yaz0"
const u8Magic = 0x55aa382d

// itemSlotName is ItemSlot.bin's name inside Common.szs.
const itemSlotName = "ItemSlot.bin"

// decompressYaz0 decompresses Yaz0 data (the compression used by .szs files).
func decompressYaz0(src []byte) ([]byte, error) {
	if len(src) < 16 || string(src[:4]) != yaz0Magic {
		return nil, errors.New("not Yaz0 data")
	}
	size := int(binary.BigEndian.Uint32(src[4:8]))
	dst := make([]byte, 0, size)
	pos := 16

	for len(dst) < size {
		if pos >= len(src) {
			return nil, errors.New("truncated Yaz0 data")
		}
		code := src[pos]
		pos++

		for bit := 7; bit >= 0 && len(dst) < size; bit-- {
			if code>>bit&1 == 1 {
				if pos >= len(src) {
					return nil, errors.New("truncated Yaz0 data")
				}
				dst = append(dst, src[pos])
				pos++
				continue
			}

			if pos+2 > len(src) {
				return nil, errors.New("truncated Yaz0 data")
			}
			b1, b2 := src[pos], src[pos+1]
			pos += 2
			dist := (int(b1&0xf)<<8 | int(b2)) + 1
			n := int(b1 >> 4)
			if n == 0 {
				if pos >= len(src) {
					return nil, errors.New("truncated Yaz0 data")
				}
				n = int(src[pos]) + 0x12
				pos++
			} else {
				n += 2
			}

			start := len(dst) - dist
			if start < 0 {
				return nil, errors.New("invalid Yaz0 back reference")
			}
			for i := range n {
				dst = append(dst, dst[start+i])
			}
		}
	}
	return dst[:size], nil
}

// findInU8 returns the first file named name in a U8 archive.
func findInU8(arc []byte, name string) ([]byte, error) {
	if len(arc) < 0x20 || binary.BigEndian.Uint32(arc[0:4]) != u8Magic {
		return nil, errors.New("not a U8 archive")
	}
	nodesOffset := int(binary.BigEndian.Uint32(arc[4:8]))
	if nodesOffset+12 > len(arc) {
		return nil, errors.New("truncated U8 archive")
	}

	// The root node's size field is the total node count. Names follow the nodes.
	count := int(binary.BigEndian.Uint32(arc[nodesOffset+8:]))
	namesOffset := nodesOffset + count*12
	if count == 0 || namesOffset > len(arc) {
		return nil, errors.New("invalid U8 node count")
	}

	for i := range count {
		node := arc[nodesOffset+i*12:]
		isDir := node[0] == 1
		nameOff := namesOffset + int(binary.BigEndian.Uint32(node[0:4])&0xffffff)
		if isDir || nameOff >= len(arc) {
			continue
		}

		end := nameOff
		for end < len(arc) && arc[end] != 0 {
			end++
		}
		if string(arc[nameOff:end]) != name {
			continue
		}

		off := int(binary.BigEndian.Uint32(node[4:8]))
		size := int(binary.BigEndian.Uint32(node[8:12]))
		if off+size > len(arc) {
			return nil, fmt.Errorf("%s is out of bounds", name)
		}
		return arc[off : off+size], nil
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// extractItemSlot returns ItemSlot.bin's bytes from either ItemSlot.bin itself or
// Common.szs (Yaz0-compressed or already decompressed).
func extractItemSlot(data []byte) ([]byte, error) {
	if len(data) >= 4 && string(data[:4]) == yaz0Magic {
		var err error
		if data, err = decompressYaz0(data); err != nil {
			return nil, err
		}
	}
	if len(data) >= 4 && binary.BigEndian.Uint32(data[:4]) == u8Magic {
		return findInU8(data, itemSlotName)
	}
	return data, nil
}
