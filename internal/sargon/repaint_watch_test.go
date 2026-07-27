package sargon

import (
	"strings"
	"testing"
)

// TestRepaintWatch drives Sargon (White, Hard, Infinite) into its first
// out-of-book search and then watches the ENTIRE move-list region (rows 10-23,
// all 40 columns) at fine granularity, logging every change with its cycle
// offset. The desync hunt showed Sargon transiently BLANKS the move-list rows
// mid-search and then repaints them; this measures how often and for how long.
func TestRepaintWatch(t *testing.T) {
	skipUnlessSlow(t)
	m, err := NewMachine(dskPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.BootToPrompt(); err != nil {
		t.Fatal(err)
	}
	m.HardMode = true
	if err := m.InfiniteLevel(); err != nil {
		t.Fatal(err)
	}
	m.Run(1_000_000)
	m.SargonWhite = true
	res, err := m.StartAsWhite(30_000_000)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sargon opens %q", res.SargonText)

	region := func() string {
		var sb strings.Builder
		for r := 10; r < 24; r++ {
			sb.WriteString(m.TextRow(r))
			sb.WriteByte('\n')
		}
		return sb.String()
	}

	// Two book replies, then the out-of-book move we watch.
	for _, mv := range []string{"D7-D6", "C8-D7"} {
		ponderWindow(m, 20_000_000)
		if _, err := m.enterMove(mv); err != nil {
			t.Fatalf("enterMove %s: %v\n%s", mv, err, m.Screen())
		}
		start := m.Cycles()
		prev := region()
		// Watch this move's whole window at fine granularity.
		for m.Cycles()-start < 150_000_000 {
			if err := m.Run(50_000); err != nil {
				t.Fatal(err)
			}
			cur := region()
			if cur != prev {
				t.Logf("[%s] +%9d cyc CHANGE:\n%s", mv, m.Cycles()-start, regionDiff(prev, cur))
				prev = cur
			}
		}
		t.Logf("[%s] watch window over; final list:\n%s", mv, region())
	}
}

func regionDiff(a, b string) string {
	ra, rb := strings.Split(a, "\n"), strings.Split(b, "\n")
	var sb strings.Builder
	for i := range ra {
		if i < len(rb) && ra[i] != rb[i] {
			sb.WriteString("    row " + string(rune('0'+(10+i)/10)) + string(rune('0'+(10+i)%10)) + ": ")
			sb.WriteString(strings.TrimRight(ra[i], " "))
			sb.WriteString("  ->  ")
			sb.WriteString(strings.TrimRight(rb[i], " "))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
