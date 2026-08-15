package format

import (
	"testing"

	ore "github.com/pixelate-it/molten/ore"
)

// One pixel per column of row 0, so a single number names it throughout.
func px(x int32, offset uint32) *ore.PixelData {
	return &ore.PixelData{X: x, Y: 0, Color: 0x112233, Offset: offset}
}

// A delta's timestamp is the flush; each pixel's offset counts back from it.
// Sorting by that recovers the order they were actually painted in, which is
// not the order they appear in the change set - a pixel enters that set the
// first time it is touched in the window.
func TestPlacementOrderSortsByOffset(t *testing.T) {
	d := &ore.Delta{
		// Recorded order 1, 2, 3; painted order 3, 1, 2.
		Changes: []*ore.PixelData{
			px(1, 500), // placed at 9500
			px(2, 100), // placed at 9900
			px(3, 900), // placed at 9100
		},
	}

	want := []int32{3, 1, 2}
	ordered := PlacementOrder(10_000, d)

	for i, x := range want {
		if ordered[i].X != x {
			t.Errorf("position %d: got x %d, want %d", i, ordered[i].X, x)
		}
	}
}

// An offset larger than the timestamp would underflow uint64 subtraction.
func TestPlacedAtClampsImplausibleOffset(t *testing.T) {
	if got := PlacedAt(100, px(1, 500)); got != 0 {
		t.Errorf("got %d, want 0 - an offset past the epoch must clamp", got)
	}
}

// Zero is an offset like any other: it names the flush instant itself.
func TestPlacedAtZeroOffsetIsTheChunkTimestamp(t *testing.T) {
	if got := PlacedAt(10_000, px(1, 0)); got != 10_000 {
		t.Errorf("got %d, want the delta's own timestamp", got)
	}
}
