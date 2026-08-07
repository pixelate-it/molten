package format

import (
	"sort"

	ore "github.com/pixelate-it/molten/ore"
)

/*
PlacedAt resolves when a pixel was actually painted, given the timestamp of the
chunk carrying it.

A delta's timestamp is the *flush*, not any individual placement - the backend
writes one delta per flush window and every pixel changed during that window
rides on it. PixelData.Offset counts milliseconds back from the chunk's
timestamp to the real moment.

Absent, the chunk's own timestamp is the best information there is, which is
what a recording written before the field looks like and what a pixel whose
time is only an upper bound falls back to.

Keyframes carry offsets too, for the same reason: a keyframe restates the whole
canvas, so without them an expansion flattens a season's placement times onto
the moment it happened.
*/
func PlacedAt(chunkTimestamp uint64, p *ore.PixelData) uint64 {
	offset := uint64(p.GetOffset())
	// A recording whose offsets outrun its own clock is corrupt rather than
	// prehistoric; clamping beats an underflow into the far future.
	if offset > chunkTimestamp {
		return 0
	}
	return chunkTimestamp - offset
}

/*
PlacementOrder returns a delta's pixels in the order they were painted, and
whether any of them carried an offset to sort by.

Recorded order is *first-touch* order within the flush window, not placement
order: a pixel enters the change set the first time it is painted and keeps
that position however often it is repainted afterwards. Anything replaying a
delta as a sequence of events - a renderer, a moderation feed - wants this
rather than the recorded slice.

A false second return means the recording predates the offset field. The caller
gets the recorded slice untouched, costing neither a copy nor a sort, and there
is no better ordering to be had.

Note this orders placements; it does not recover them. A delta carries the
*state* of each changed pixel at flush time, so five repaints of one pixel
inside one window are one entry with the final colour.
*/
func PlacementOrder(d *ore.Delta) ([]*ore.PixelData, bool) {
	hasOffsets := false
	for _, p := range d.Changes {
		if p.Offset != nil {
			hasOffsets = true
			break
		}
	}

	if !hasOffsets {
		return d.Changes, false
	}

	ordered := make([]*ore.PixelData, len(d.Changes))
	copy(ordered, d.Changes)

	// Stable, so pixels sharing an instant keep their recorded order.
	sort.SliceStable(ordered, func(i, j int) bool {
		return PlacedAt(d.Timestamp, ordered[i]) < PlacedAt(d.Timestamp, ordered[j])
	})

	return ordered, true
}
