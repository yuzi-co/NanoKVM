package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

// stereoTone builds duration worth of interleaved 48 kHz stereo S16_LE at the
// given frequency, the same signal in both channels.
func stereoTone(frequency float64, samples int) []byte {
	pcm := make([]byte, samples*4)

	for i := 0; i < samples; i++ {
		value := int16(10000 * math.Sin(2*math.Pi*frequency*float64(i)/float64(InputRate)))

		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(value))
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(value))
	}

	return pcm
}

// peak reports the largest absolute sample, ignoring the filter's settling
// time at the start of the signal.
func peak(samples []int16) float64 {
	var highest float64

	for _, sample := range samples[len(samples)/2:] {
		if magnitude := math.Abs(float64(sample)); magnitude > highest {
			highest = magnitude
		}
	}

	return highest
}

func TestProcessProducesOneSamplePerSixInputFrames(t *testing.T) {
	// 960 stereo frames is 20 ms at 48 kHz, which is 3840 bytes.
	pcm := stereoTone(1000, 960)

	got := NewDecimator().Process(pcm, nil)

	if len(got) != 160 {
		t.Errorf("got %d samples from 20 ms of audio, want 160", len(got))
	}
}

func TestProcessKeepsFilterStateAcrossChunks(t *testing.T) {
	pcm := stereoTone(1000, 960)
	decimator := NewDecimator()

	first := decimator.Process(pcm[:len(pcm)/2], nil)
	second := decimator.Process(pcm[len(pcm)/2:], nil)

	if len(first)+len(second) != 160 {
		t.Errorf("two half chunks produced %d samples, want 160", len(first)+len(second))
	}
}

func TestProcessPassesThePassband(t *testing.T) {
	// One second, so the measurement does not depend on settling.
	pcm := stereoTone(1000, InputRate)

	got := NewDecimator().Process(pcm, nil)

	if p := peak(got); p < 9000 {
		t.Errorf("1 kHz tone came through at peak %.0f, want at least 9000 of 10000", p)
	}
}

// This is the reason the filter exists. Without it, 6 kHz would alias down to
// 2 kHz and be plainly audible.
func TestProcessRejectsAboveNyquist(t *testing.T) {
	pcm := stereoTone(6000, InputRate)

	got := NewDecimator().Process(pcm, nil)

	if p := peak(got); p > 300 {
		t.Errorf("6 kHz tone came through at peak %.0f, want under 300 of 10000", p)
	}
}

// 6 kHz is an easy target: it sits far into the stopband of even a short
// filter. The band that actually decides how many taps are needed is just
// above Nyquist, where a lazy filter is only a few dB down and the result
// folds back on top of speech.
func TestProcessRejectsJustAboveNyquist(t *testing.T) {
	pcm := stereoTone(4500, InputRate)

	got := NewDecimator().Process(pcm, nil)

	if p := peak(got); p > 300 {
		t.Errorf("4.5 kHz tone came through at peak %.0f, want under 300 of 10000", p)
	}
}

func TestProcessDownmixesToMono(t *testing.T) {
	// Left at +8000, right at -8000. A correct downmix cancels to silence.
	pcm := make([]byte, 960*4)
	rightVal := int16(-8000)
	for i := 0; i < 960; i++ {
		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(int16(8000)))
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(rightVal))
	}

	got := NewDecimator().Process(pcm, nil)

	if p := peak(got); p > 1 {
		t.Errorf("opposed channels produced peak %.0f, want silence", p)
	}
}
