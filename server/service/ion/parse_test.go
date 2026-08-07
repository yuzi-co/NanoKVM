package ion

import "testing"

// summaryIdle is a fresh boot with capture never started. It keeps the trailing
// "free memory regions" block, which proves the parser stops at the end of the
// Details table instead of reading rows out of it.
const summaryIdle = `Summary:
[0] carveout heap size:78643200 bytes, used:19050496 bytes
usage rate:25%, memory usage peak 19050496 bytes

Details:
         heap_id   alloc_buf_size         phy_addr         kmap_cnt      buffer name
               0         12533760         8b300000                1          VbPool0
               0          6221824         8bf3c000                1          VbPool1
               0           294912         8bef4000                1 ISP_SHARED_BUFFER_0


minimum ion allocate unit = 4096
free memory regions:
         heap_id            start              end           length
               0         8c52b000         8fe00000         59592704
`

// summaryTwoGenerations was produced deliberately by restarting the server. The
// two ISP_SHARED_BUFFER_0 entries at different phy_addr are the whole point:
// one belongs to the live process and one to the process that died holding it.
const summaryTwoGenerations = `Summary:
[0] carveout heap size:78643200 bytes, used:49459200 bytes
usage rate:62%, memory usage peak 49459200 bytes

Details:
         heap_id   alloc_buf_size         phy_addr         kmap_cnt      buffer name
               0          9437184         8cffc000                1         jpeg_ion
               0          6221824         8dc3c000                1          VbPool4
               0          3112960         8d8fc000                1          VbPool3
               0         12533760         8b300000                1          VbPool0
               0          3133440         8c6ff000                1          VbPool2
               0            81920         8c6eb000                1 VENC_1_H264_WorkBuffer
               0          6221824         8bf3c000                1          VbPool1
               0           786432         8c52b000                1 VCODEC_H264_FW_Buffer
               0           294912         8dbf4000                1 ISP_SHARED_BUFFER_0
               0          3145728         8ccfc000                1 VENC_1_ReconFrameBuf
               0          3145728         8c9fc000                1 VENC_1_ReconFrameBuf
               0          1048576         8c5eb000                1 VENC_1_BitStreamBuffer
               0           294912         8bef4000                1 ISP_SHARED_BUFFER_0


minimum ion allocate unit = 4096
`

func TestParseCounter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want uint64
		bad  bool
	}{
		{"plain", "19050496", 19050496, false},
		{"trailing newline", "78643200\n", 78643200, false},
		{"zero", "0", 0, false},
		{"empty", "", 0, true},
		{"blank", "   \n", 0, true},
		{"not a number", "nineteen", 0, true},
		{"negative", "-1", 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCounter(c.in)
			if c.bad {
				if err == nil {
					t.Fatalf("ParseCounter(%q) = %d, want an error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCounter(%q): %s", c.in, err)
			}
			if got != c.want {
				t.Fatalf("ParseCounter(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestParseSummaryReadsOnlyTheDetailsTable(t *testing.T) {
	bufs, err := ParseSummary(summaryIdle)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}
	if len(bufs) != 3 {
		t.Fatalf("got %d buffers, want 3: %+v", len(bufs), bufs)
	}

	want := []Buffer{
		{HeapID: 0, Size: 12533760, PhyAddr: "8b300000", Name: "VbPool0"},
		{HeapID: 0, Size: 6221824, PhyAddr: "8bf3c000", Name: "VbPool1"},
		{HeapID: 0, Size: 294912, PhyAddr: "8bef4000", Name: "ISP_SHARED_BUFFER_0"},
	}
	for i := range want {
		if bufs[i] != want[i] {
			t.Fatalf("buffer %d = %+v, want %+v", i, bufs[i], want[i])
		}
	}
}

func TestParseSummaryTotalsAgreeWithTheHeader(t *testing.T) {
	bufs, err := ParseSummary(summaryIdle)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}

	var sum uint64
	for _, b := range bufs {
		sum += b.Size
	}
	if sum != 19050496 {
		t.Fatalf("buffer sizes total %d, want 19050496 to match the header", sum)
	}
}

func TestCountGenerations(t *testing.T) {
	one, err := ParseSummary(summaryIdle)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}
	if got := CountGenerations(one); got != 1 {
		t.Fatalf("idle board: CountGenerations = %d, want 1", got)
	}

	two, err := ParseSummary(summaryTwoGenerations)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}
	if got := CountGenerations(two); got != 2 {
		t.Fatalf("after one restart: CountGenerations = %d, want 2", got)
	}
}

func TestParseSummaryOnRubbish(t *testing.T) {
	for _, in := range []string{"", "Summary:\n", "Details:\n", "not a summary at all\n"} {
		bufs, err := ParseSummary(in)
		if err != nil {
			t.Fatalf("ParseSummary(%q) returned an error: %s", in, err)
		}
		if len(bufs) != 0 {
			t.Fatalf("ParseSummary(%q) = %+v, want no buffers", in, bufs)
		}
	}
}

func TestVerdict(t *testing.T) {
	const reserve = 12550144

	cases := []struct {
		name    string
		free    uint64
		reserve uint64
		want    string
	}{
		{"plenty", reserve * 4, reserve, VerdictOK},
		{"exactly twice the reserve is ok", reserve * 2, reserve, VerdictOK},
		{"one byte under twice is warn", reserve*2 - 1, reserve, VerdictWarn},
		{"exactly the reserve is warn", reserve, reserve, VerdictWarn},
		{"one byte under the reserve is critical", reserve - 1, reserve, VerdictCritical},
		{"nothing free is critical", 0, reserve, VerdictCritical},
		{"no reserve is unavailable", reserve * 4, 0, VerdictUnavailable},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Verdict(c.free, c.reserve); got != c.want {
				t.Fatalf("Verdict(%d, %d) = %q, want %q", c.free, c.reserve, got, c.want)
			}
		})
	}
}
