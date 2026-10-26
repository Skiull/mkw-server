package records

// bitReader reads big-endian, MSB-first bitfields. Several records (RaceMode, RaceData) pack
// fields that aren't byte aligned.
type bitReader struct {
	data []byte
	pos  int // bit position
}

func newBitReader(data []byte) *bitReader {
	return &bitReader{data: data}
}

// read returns the next n bits (n <= 32). Bits past the end of data read as 0, so callers
// must check remaining() when the length isn't fixed.
func (b *bitReader) read(n int) uint32 {
	var v uint32
	for range n {
		byteIdx := b.pos / 8
		var bit uint32
		if byteIdx < len(b.data) {
			bit = uint32(b.data[byteIdx]>>(7-b.pos%8)) & 1
		}
		v = v<<1 | bit
		b.pos++
	}
	return v
}

func (b *bitReader) remaining() int {
	return len(b.data)*8 - b.pos
}

// readBits reads n bits starting at an absolute bit offset without a reader.
func readBits(data []byte, bitOffset, n int) uint32 {
	r := &bitReader{data: data, pos: bitOffset}
	return r.read(n)
}
