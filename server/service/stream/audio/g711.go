package audio

// Mu-law encoding, ITU-T G.711. The bias is added so that the exponent search
// below finds a segment for every input, including silence.
const (
	uLawBias = 0x84
	uLawClip = 32635
)

// EncodeULaw appends one mu-law byte per sample to dst and returns the
// extended slice. Passing dst[:0] reuses the caller's buffer.
func EncodeULaw(samples []int16, dst []byte) []byte {
	for _, sample := range samples {
		dst = append(dst, encodeULawSample(sample))
	}

	return dst
}

func encodeULawSample(sample int16) byte {
	// int, not int16: negating math.MinInt16 does not fit in an int16.
	value := int(sample)

	var sign byte
	if value < 0 {
		value = -value
		sign = 0x80
	}

	if value > uLawClip {
		value = uLawClip
	}

	value += uLawBias

	// Find the highest set bit from 0x4000 down to 0x80. That bit selects the
	// segment, and the four bits under it are the mantissa.
	exponent := 7
	for mask := 0x4000; exponent > 0 && value&mask == 0; mask >>= 1 {
		exponent--
	}

	mantissa := (value >> (exponent + 3)) & 0x0F

	return ^(sign | byte(exponent<<4) | byte(mantissa))
}
