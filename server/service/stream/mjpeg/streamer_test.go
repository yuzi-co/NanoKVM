package mjpeg

import (
	"testing"
)

func TestCachingAFrameDoesNotAllocate(t *testing.T) {
	// ReadMjpeg already hands back a freshly allocated, uniquely owned slice
	// per frame, and nothing here mutates it. Copying it again is a full JPEG
	// memcpy per frame for a consumer that reads one every few seconds.
	s := NewStreamer()
	s.enableLatestFrameCache()

	frame := make([]byte, 64*1024)

	allocs := testing.AllocsPerRun(50, func() {
		s.setLatestFrame(frame, 1920, 1080)
	})

	if allocs != 0 {
		t.Fatalf("expected caching a frame to allocate nothing, got %v allocations", allocs)
	}
}

func TestCachedFrameIsReturnedWithItsDimensions(t *testing.T) {
	s := NewStreamer()
	s.enableLatestFrameCache()

	s.setLatestFrame([]byte{1, 2, 3}, 1920, 1080)

	frame, ok := s.getLatestFrame()
	if !ok {
		t.Fatal("expected a cached frame")
	}

	if string(frame.Data) != string([]byte{1, 2, 3}) {
		t.Fatalf("expected the frame data, got %v", frame.Data)
	}

	if frame.Width != 1920 || frame.Height != 1080 {
		t.Fatalf("expected 1920x1080, got %dx%d", frame.Width, frame.Height)
	}
}

func TestReadersCannotCorruptTheCachedFrame(t *testing.T) {
	// The cache is shared with the capture loop now, so the read side must
	// keep handing out copies.
	s := NewStreamer()
	s.enableLatestFrameCache()

	s.setLatestFrame([]byte{1, 2, 3}, 1920, 1080)

	first, _ := s.getLatestFrame()
	first.Data[0] = 9

	second, _ := s.getLatestFrame()
	if second.Data[0] != 1 {
		t.Fatal("expected a caller to be unable to corrupt the cached frame")
	}
}

func TestDisablingTheCacheClearsTheFrame(t *testing.T) {
	s := NewStreamer()
	s.enableLatestFrameCache()
	s.setLatestFrame([]byte{1, 2, 3}, 1920, 1080)

	s.disableLatestFrameCache()

	if _, ok := s.getLatestFrame(); ok {
		t.Fatal("expected the cached frame to be dropped")
	}
}
