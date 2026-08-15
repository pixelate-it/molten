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

Zero is the ordinary answer for a pixel painted inside the same millisecond as
its flush, and the only answer for one whose real time nobody recorded. Both
resolve to the chunk's timestamp, which is not a guess: a Delta's timestamp is
a real instant, and the pixel was on the canvas by then.
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
PlacementOrder returns a delta's pixels in the order they were painted.

Recorded order is *first-touch* order within the flush window, not placement
order: a pixel enters the change set the first time it is painted and keeps
that position however often it is repainted afterwards. Anything replaying a
delta as a sequence of events - a renderer, a moderation feed - wants this
rather than the recorded slice.

The timestamp is passed in rather than read off the delta: it lives on the
enclosing RecordingChunk now, so a caller that has the delta has necessarily
also seen the chunk it came out of.

Note this orders placements; it does not recover them. A delta carries the
*state* of each changed pixel at flush time, so five repaints of one pixel
inside one window are one entry with the final colour.
*/
func PlacementOrder(chunkTimestamp uint64, d *ore.Delta) []*ore.PixelData {
	ordered := make([]*ore.PixelData, len(d.Changes))
	copy(ordered, d.Changes)

	// Stable, so pixels sharing an instant keep their recorded order.
	sort.SliceStable(ordered, func(i, j int) bool {
		return PlacedAt(chunkTimestamp, ordered[i]) <
			PlacedAt(chunkTimestamp, ordered[j])
	})

	return ordered
}
