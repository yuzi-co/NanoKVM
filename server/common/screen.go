package common

import "sync"

// ScreenValues is a consistent copy of the capture parameters.
type ScreenValues struct {
	Width   uint16
	Height  uint16
	FPS     int
	Quality uint16
	BitRate uint16
	GOP     uint8
}

// Screen holds the capture parameters. HTTP handlers write them while the
// streamer goroutines read them on every frame, so access goes through the
// mutex and readers take a snapshot instead of reading field by field.
type Screen struct {
	mutex  sync.RWMutex
	values ScreenValues
}

var (
	screen     *Screen
	screenOnce sync.Once
)

// ResolutionMap height to width
var ResolutionMap = map[uint16]uint16{
	1080: 1920,
	720:  1280,
	600:  800,
	480:  640,
	0:    0,
}

var QualityMap = map[uint16]bool{
	100: true,
	80:  true,
	60:  true,
	50:  true,
}

var BitRateMap = map[uint16]bool{
	5000: true,
	3000: true,
	2000: true,
	1000: true,
}

func GetScreen() *Screen {
	screenOnce.Do(func() {
		screen = &Screen{
			values: ScreenValues{
				Width:   0,
				Height:  0,
				Quality: 80,
				FPS:     30,
				BitRate: 3000,
				GOP:     30,
			},
		}
	})

	return screen
}

// Snapshot returns the current parameters as a single consistent copy.
func (s *Screen) Snapshot() ScreenValues {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return s.values
}

func SetScreen(key string, value int) {
	s := GetScreen()

	s.mutex.Lock()
	defer s.mutex.Unlock()

	switch key {
	case "resolution":
		height := uint16(value)
		if width, ok := ResolutionMap[height]; ok {
			s.values.Width = width
			s.values.Height = height
		}

	case "quality":
		if value > 100 {
			s.values.BitRate = uint16(value)
		} else {
			s.values.Quality = uint16(value)
		}

	case "fps":
		s.values.FPS = validateFPS(value)

	case "gop":
		s.values.GOP = uint8(value)
	}
}

func CheckScreen() {
	s := GetScreen()

	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, ok := ResolutionMap[s.values.Height]; !ok {
		s.values.Width = 1920
		s.values.Height = 1080
	}

	if _, ok := QualityMap[s.values.Quality]; !ok {
		s.values.Quality = 80
	}

	if _, ok := BitRateMap[s.values.BitRate]; !ok {
		s.values.BitRate = 3000
	}
}

func validateFPS(fps int) int {
	if fps > 60 {
		return 60
	}
	if fps < 10 {
		return 10
	}

	return fps
}
