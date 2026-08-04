package audio

import (
	"math"
	"testing"
)

// The four values below are fixed by ITU-T G.711: silence encodes to 0xFF,
// full-scale positive to 0x80, and full-scale negative to 0x00.
func TestEncodeULawMatchesReferenceValues(t *testing.T) {
	cases := []struct {
		name   string
		sample int16
		want   byte
	}{
		{"silence", 0, 0xFF},
		{"smallest negative", -1, 0x7F},
		{"full scale positive", math.MaxInt16, 0x80},
		{"full scale negative", math.MinInt16, 0x00},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EncodeULaw([]int16{c.sample}, nil)

			if len(got) != 1 {
				t.Fatalf("got %d bytes, want 1", len(got))
			}

			if got[0] != c.want {
				t.Errorf("sample %d encoded to %#02x, want %#02x", c.sample, got[0], c.want)
			}
		})
	}
}

func TestEncodeULawAppendsToDestination(t *testing.T) {
	dst := make([]byte, 0, 4)
	dst = append(dst, 0x11)

	got := EncodeULaw([]int16{0, 0}, dst)

	if len(got) != 3 {
		t.Fatalf("got %d bytes, want 3", len(got))
	}

	if got[0] != 0x11 {
		t.Errorf("existing byte was overwritten with %#02x", got[0])
	}
}

// A sign flip must not change the magnitude bits, only the sign bit. Both
// halves of the code are inverted, so the two encodings differ by 0x80.
func TestEncodeULawIsSymmetric(t *testing.T) {
	positive := EncodeULaw([]int16{4096}, nil)[0]
	negative := EncodeULaw([]int16{-4096}, nil)[0]

	if positive^negative != 0x80 {
		t.Errorf("%#02x and %#02x differ by more than the sign bit", positive, negative)
	}
}
