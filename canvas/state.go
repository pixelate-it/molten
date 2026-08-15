/*
Package canvas replays a recording's chunks into something you can look at.

Two projections of the same history, because they answer different questions:

  - [State] keeps the canvas as an image, for rendering or for asking what
    colour a pixel is at some moment.
  - [Digest] keeps colour and attribution without an image, for reproducing
    Footer.canvas_checksum - the format's only cross-implementation check.

Neither reads a file. Feed them chunks from package format.

One rule both share, and the easiest thing to get wrong in a new reader: a
pixel names a coordinate on a fixed plane, not a place in the canvas, and the
canvas is a window onto that plane which moves. So the canvas' corner has to
come from the last Resize seen, not from the first one and not from zero - a
season expanded leftwards puts its corner in the negative, and a reader that
assumes (0, 0) draws every pixel in the wrong place.

Which is also why a Resize carries no pixels: everything already applied keeps
its coordinate, so the window moves and the contents come along. Both types
below implement that as Reframe.
*/
package canvas

import (
	"image"

	"github.com/pixelate-it/molten/format"
	ore "github.com/pixelate-it/molten/ore"
)

// State is a canvas replayed into an RGBA image.
type State struct {
	Width, Height uint32

	// MinX and MinY are the canvas' top-left corner, in the coordinates the
	// pixels are named by. Negative once a season has grown left or up.
	MinX, MinY int32

	img *image.RGBA
}

// NewState returns a canvas with no size yet. Nothing can be applied until a
// sizing keyframe arrives - see [State.Ready].
func NewState() *State {
	return &State{}
}

// Ready reports whether a size has been established. A recording's first
// keyframe is what does that, so this is false only before it has been applied
// (or on a file so truncated it never arrives).
func (s *State) Ready() bool {
	return s.Width > 0 && s.Height > 0
}

/*
Reframe moves the canvas onto a new window, keeping every pixel still inside
it and filling the rest with blank.

This is what a Resize chunk applies, and why that chunk can carry no pixels: a
coordinate means the same pixel on both sides, so nothing is re-addressed and
nothing has to be restated. Only the rectangle the pixels are drawn into moves.

Until v7 a resize was a Keyframe carrying the whole painted canvas, and a
reader blanked and rebuilt from it. On a real season that cost 91% of the file.

Blank is white and fully opaque. Anything else and the area an expansion adds
comes out transparent.
*/
func (s *State) Reframe(w format.Window) {
	img := image.NewRGBA(image.Rect(0, 0, int(w.Width), int(w.Height)))

	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = 0xff
		img.Pix[i+1] = 0xff
		img.Pix[i+2] = 0xff
		img.Pix[i+3] = 0xff
	}

	if s.img != nil {
		// Where the old buffer's rows land in the new one.
		shiftX := int(s.MinX - w.MinX)
		shiftY := int(s.MinY - w.MinY)

		/* Clipped per axis before the copy: a row hanging off one side is
		 * trimmed, not dropped - a cut on one axis only would otherwise lose
		 * rows it kept every pixel of. */
		from := max(0, -shiftX)
		until := min(int(s.Width), int(w.Width)-shiftX)

		for row := 0; row < int(s.Height) && until > from; row++ {
			movedRow := row + shiftY
			if movedRow < 0 || movedRow >= int(w.Height) {
				continue
			}

			src := s.img.PixOffset(from, row)
			dst := img.PixOffset(from+shiftX, movedRow)

			copy(img.Pix[dst:dst+(until-from)*4], s.img.Pix[src:])
		}
	}

	s.Width, s.Height = w.Width, w.Height
	s.MinX, s.MinY = w.MinX, w.MinY
	s.img = img
}

// ApplyResize applies a Resize chunk. Callers that cut a video segment on a
// size change watch for this and nothing else.
func (s *State) ApplyResize(r *ore.Resize) {
	s.Reframe(format.ReadWindow(r))
}

func (s *State) applyPixel(p *ore.PixelData) {
	if s.img == nil {
		return
	}
	/* Per axis, and it has to be: a column past the right edge is still a
	 * valid offset into the image - one row down, in the first columns - so a
	 * single check against the area would smear a pixel along the left edge
	 * instead of dropping it. */
	col := int(p.X - s.MinX)
	row := int(p.Y - s.MinY)
	if col < 0 || row < 0 || col >= int(s.Width) || row >= int(s.Height) {
		return
	}

	off := s.img.PixOffset(col, row)

	s.img.Pix[off+0] = byte(p.Color >> 16)
	s.img.Pix[off+1] = byte(p.Color >> 8)
	s.img.Pix[off+2] = byte(p.Color)
	s.img.Pix[off+3] = 0xff
}

// ApplyDelta applies every change in a delta, in recorded order. To walk them
// in the order they were painted instead, range over format.PlacementOrder and
// use [State.ApplySinglePixel].
func (s *State) ApplyDelta(d *ore.Delta) {
	for _, p := range d.Changes {
		s.applyPixel(p)
	}
}

// ApplySinglePixel applies one pixel, for a caller stepping through a delta
// itself - a renderer emitting frames between placements, or a moderation pass
// that has to look at the canvas after each change.
func (s *State) ApplySinglePixel(p *ore.PixelData) {
	s.applyPixel(p)
}

// ColorAt returns the colour at (x, y) as 0x00RRGGBB, and false if the point is
// outside the canvas or nothing has sized it yet. The coordinates are the
// pixels' own, so they are signed and need not start at the origin.
func (s *State) ColorAt(x, y int) (uint32, bool) {
	col := x - int(s.MinX)
	row := y - int(s.MinY)

	if s.img == nil || col < 0 || row < 0 || col >= int(s.Width) || row >= int(s.Height) {
		return 0, false
	}

	off := s.img.PixOffset(col, row)
	return uint32(s.img.Pix[off+0])<<16 |
		uint32(s.img.Pix[off+1])<<8 |
		uint32(s.img.Pix[off+2]), true
}

// Image returns the live image, not a copy: applying anything else writes
// through it. Copy it before handing it somewhere that outlives the next chunk.
func (s *State) Image() *image.RGBA {
	return s.img
}
