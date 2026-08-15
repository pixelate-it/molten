package canvas

import (
	"testing"

	"github.com/pixelate-it/molten/format"
	ore "github.com/pixelate-it/molten/ore"
)

func u32(v uint32) *uint32 { return &v }
func u64(v uint64) *uint64 { return &v }

func pixel(x, y int32, colour uint32) *ore.PixelData {
	return &ore.PixelData{X: x, Y: y, Color: colour}
}

func placed(x, y int32, colour uint32, author uint64) *ore.PixelData {
	return &ore.PixelData{X: x, Y: y, Color: colour, Author: u64(author)}
}

/*
The property the whole checksum design exists for.

A resize moves the canvas around the artwork without moving the artwork, so a
digest over slots in the buffer would change purely because the width did.
Hashing the pixels' own coordinates is what makes the two agree - and what lets
a second implementation, replaying the same file, arrive at the same number.
*/
func TestSumIsStableAcrossAResize(t *testing.T) {
	// 4x4 starting on the origin, artwork at (1,1) and (2,3)
	before := NewDigest()
	before.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	before.Apply(placed(1, 1, 0xff0000, 7))
	before.Apply(placed(2, 3, 0x00ff00, 7))

	// grown to 8x8 away from the artwork, so the canvas now reaches two pixels
	// past where the season started. The artwork is where it always was.
	after := NewDigest()
	after.Reframe(format.Window{Width: 8, Height: 8, MinX: -2, MinY: -2})
	after.Apply(placed(1, 1, 0xff0000, 7))
	after.Apply(placed(2, 3, 0x00ff00, 7))

	if before.Sum() != after.Sum() {
		t.Fatalf("same artwork hashed differently either side of a resize: %016x vs %016x",
			before.Sum(), after.Sum())
	}
}

// Different artwork must not collide, or the check above proves nothing.
func TestSumDistinguishesArtwork(t *testing.T) {
	a := NewDigest()
	a.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	a.Apply(placed(1, 1, 0xff0000, 7))

	b := NewDigest()
	b.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	b.Apply(placed(1, 1, 0xff0001, 7))

	if a.Sum() == b.Sum() {
		t.Fatal("a one-bit colour difference hashed the same")
	}
}

/*
"Non-blank" has to mean exactly what a keyframe means by it, or a reader that
rebuilt the canvas from a keyframe cannot reproduce the digest: white with
nobody's name on it is skipped, white that somebody placed is not.
*/
func TestSumCountsAttributedWhiteAndSkipsBlank(t *testing.T) {
	blank := NewDigest()
	blank.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})

	untouched := NewDigest()
	untouched.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	untouched.Apply(pixel(1, 1, BlankColour)) // white, no author, no tag

	if blank.Sum() != untouched.Sum() {
		t.Error("an unattributed white pixel is canvas, not artwork - it must not be digested")
	}

	attributed := NewDigest()
	attributed.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	attributed.Apply(placed(1, 1, BlankColour, 7))

	if attributed.Sum() == blank.Sum() {
		t.Error("white somebody deliberately placed is artwork and must be digested")
	}
}

// Attribution is assigned, not accumulated: repainting a pixel with no author
// genuinely clears it, and the writer records it that way.
func TestApplyClearsAttribution(t *testing.T) {
	cleared := NewDigest()
	cleared.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	cleared.Apply(placed(1, 1, BlankColour, 7))
	cleared.Apply(pixel(1, 1, BlankColour))

	blank := NewDigest()
	blank.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})

	if cleared.Sum() != blank.Sum() {
		t.Error("a repaint with no author must clear the attribution, leaving blank canvas")
	}
}

// A Resize says only where the canvas now is; everything inside it keeps its
// coordinate and comes along.
func TestApplyResizeMovesTheWindowAndKeepsTheArtwork(t *testing.T) {
	d := NewDigest()
	d.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	d.Apply(placed(1, 1, 0xff0000, 7))

	d.ApplyResize(&ore.Resize{
		Width: 8, Height: 8, MinX: -2, MinY: -2,
	})

	w, h := d.Size()
	x, y := d.Corner()
	if w != 8 || h != 8 || x != -2 || y != -2 {
		t.Fatalf("got %dx%d at %d,%d, want 8x8 at -2,-2", w, h, x, y)
	}

	/* Carried across, not blanked away: the pixel at (1, 1) is inside the new
	 * window, so it is still there and still counted. That is the whole reason
	 * a Resize can carry no pixels. */
	kept := NewDigest()
	kept.Reframe(format.Window{Width: 8, Height: 8, MinX: -2, MinY: -2})
	kept.Apply(placed(1, 1, 0xff0000, 7))

	if d.Sum() != kept.Sum() {
		t.Error("the pixel from before the resize should have survived it")
	}
}

// A cut destroys what falls outside, and every reader has to drop it itself -
// under the old scheme the resize keyframe simply omitted it.
func TestDigestReframeDropsWhatFallsOutside(t *testing.T) {
	d := NewDigest()
	d.Reframe(format.Window{Width: 8, Height: 8})
	d.Apply(placed(1, 1, 0xff0000, 7))
	d.Apply(placed(6, 6, 0x00ff00, 7))

	// Keep the bottom-right quadrant: (1, 1) is gone, (6, 6) survives.
	d.Reframe(format.Window{Width: 4, Height: 4, MinX: 4, MinY: 4})

	kept := NewDigest()
	kept.Reframe(format.Window{Width: 4, Height: 4, MinX: 4, MinY: 4})
	kept.Apply(placed(6, 6, 0x00ff00, 7))

	if d.Sum() != kept.Sum() {
		t.Error("a pixel outside the new window must be dropped")
	}
}

// A keyframe carries contents and no geometry, so it can never move anything.
func TestApplyKeyframeLeavesTheWindowAlone(t *testing.T) {
	d := NewDigest()
	d.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})

	d.ApplyKeyframe(&ore.Keyframe{
		Pixels: []*ore.PixelData{placed(1, 1, 0xff0000, 7)},
	})

	w, h := d.Size()
	if w != 4 || h != 4 {
		t.Fatalf("got %dx%d, want the size left alone", w, h)
	}
}

// Coordinates off the canvas are dropped rather than panicking: a truncated
// recording, or one whose corner this reader got wrong, must not take it down.
func TestApplyIgnoresCoordinatesOffTheCanvas(t *testing.T) {
	d := NewDigest()
	d.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})

	blank := d.Sum()

	d.Apply(placed(9999, 9999, 0xff0000, 7))
	// Negative, which is a real coordinate elsewhere but not on this canvas -
	// and the case a single check against the buffer length would let through
	// as a wrap onto some other pixel.
	d.Apply(placed(-1, 0, 0xff0000, 7))
	d.Apply(placed(0, -1, 0xff0000, 7))
	// One column past the right edge, which is a valid slot one row down.
	d.Apply(placed(4, 0, 0xff0000, 7))

	if d.Sum() != blank {
		t.Error("a pixel outside the canvas must be dropped")
	}
}
