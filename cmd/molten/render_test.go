package main

import (
	"testing"

	ore "github.com/pixelate-it/molten/ore"
)

func px(id uint32, offset *uint32) *ore.PixelData {
	return &ore.PixelData{Id: id, Color: 0x112233, Offset: offset}
}

func u32(v uint32) *uint32 { return &v }

// A delta's timestamp is the flush; each pixel's offset counts back from it.
// Sorting by that recovers the order they were actually painted in, which is
// not the order they appear in the change set - a pixel enters that set the
// first time it is touched in the window.
func TestPlacementOrderSortsByOffset(t *testing.T) {
	d := &ore.Delta{
		Timestamp: 10_000,
		// Recorded order 1, 2, 3; painted order 3, 1, 2.
		Changes: []*ore.PixelData{
			px(1, u32(500)), // placed at 9500
			px(2, u32(100)), // placed at 9900
			px(3, u32(900)), // placed at 9100
		},
	}

	ordered, timed := placementOrder(d)

	if !timed {
		t.Fatal("expected offsets to be detected")
	}

	want := []uint32{3, 1, 2}
	for i, id := range want {
		if ordered[i].Id != id {
			t.Errorf("position %d: got id %d, want %d", i, ordered[i].Id, id)
		}
	}
}

// Recordings written before the field must behave exactly as they did.
func TestPlacementOrderWithoutOffsetsIsUntouched(t *testing.T) {
	d := &ore.Delta{
		Timestamp: 10_000,
		Changes:   []*ore.PixelData{px(1, nil), px(2, nil), px(3, nil)},
	}

	ordered, timed := placementOrder(d)

	if timed {
		t.Fatal("no pixel carries an offset, so nothing can be ordered by it")
	}
	if &ordered[0] != &d.Changes[0] {
		t.Error("expected the recorded slice back, not a copy")
	}
}

// An offset larger than the timestamp would underflow uint64 subtraction.
func TestPlacedAtClampsImplausibleOffset(t *testing.T) {
	d := &ore.Delta{Timestamp: 100}

	if got := placedAt(d, px(1, u32(500))); got != 0 {
		t.Errorf("got %d, want 0 - an offset past the epoch must clamp", got)
	}
}

func TestPlacedAtFallsBackToChunkTimestamp(t *testing.T) {
	d := &ore.Delta{Timestamp: 10_000}

	if got := placedAt(d, px(1, nil)); got != 10_000 {
		t.Errorf("got %d, want the delta's own timestamp", got)
	}
}
