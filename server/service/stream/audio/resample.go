package audio

import "math"

const (
	// InputRate, OutputRate and Decimation describe the only conversion this
	// package performs. The gadget offers 48 kHz stereo and nothing else.
	InputRate  = 48000
	OutputRate = 8000
	Decimation = InputRate / OutputRate

	// filterTaps and cutoffHz put the half-gain point at 3.4 kHz and the
	// stopband at about 4 kHz, which is Nyquist for the 8 kHz output.
	//
	// The tap count follows from the transition width. A Hamming window needs
	// roughly 3.3*rate/width taps, so the 1.2 kHz transition either side of
	// 3.4 kHz needs about 130. Fewer taps do not fail loudly: they leave 4 to
	// 5 kHz only a few dB down, and that band folds straight back into speech.
	// 129 taps cost about one million multiplies per second, which this board
	// can afford.
	filterTaps = 129
	cutoffHz   = 3400.0
)

// Decimator converts interleaved 48 kHz stereo S16_LE into 8 kHz mono samples.
// One instance belongs to one stream: it carries filter state between calls.
type Decimator struct {
	coeffs  []float64
	history []float64
	next    int
	phase   int
}

func NewDecimator() *Decimator {
	return &Decimator{
		coeffs:  lowPassCoefficients(filterTaps, cutoffHz, InputRate),
		history: make([]float64, filterTaps),
	}
}

// Process appends 8 kHz mono samples to out and returns the extended slice.
// Passing out[:0] reuses the caller's buffer.
func (d *Decimator) Process(pcm []byte, out []int16) []int16 {
	for i := 0; i+3 < len(pcm); i += 4 {
		left := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		right := int16(uint16(pcm[i+2]) | uint16(pcm[i+3])<<8)

		d.history[d.next] = (float64(left) + float64(right)) / 2
		d.next = (d.next + 1) % len(d.history)

		d.phase++
		if d.phase < Decimation {
			continue
		}
		d.phase = 0

		out = append(out, d.filter())
	}

	return out
}

// filter runs the FIR over the history ring. history[next] is the oldest
// sample, so the walk starts there and the coefficients line up in order.
func (d *Decimator) filter() int16 {
	var sum float64

	index := d.next
	for _, coeff := range d.coeffs {
		sum += coeff * d.history[index]
		index = (index + 1) % len(d.history)
	}

	if sum > math.MaxInt16 {
		return math.MaxInt16
	}

	if sum < math.MinInt16 {
		return math.MinInt16
	}

	return int16(sum)
}

// lowPassCoefficients builds a Hamming-windowed sinc and normalises it to unity
// gain at DC, so a constant input comes out at the same level.
func lowPassCoefficients(taps int, cutoff, rate float64) []float64 {
	coeffs := make([]float64, taps)
	middle := float64(taps-1) / 2
	omega := 2 * math.Pi * cutoff / rate

	var sum float64

	for i := range coeffs {
		n := float64(i) - middle

		value := omega
		if n != 0 {
			value = math.Sin(omega*n) / n
		}

		window := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(taps-1))

		coeffs[i] = value * window
		sum += coeffs[i]
	}

	for i := range coeffs {
		coeffs[i] /= sum
	}

	return coeffs
}
