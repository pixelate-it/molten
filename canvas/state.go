/*
Package canvas replays a recording's chunks into something you can look at.

Two projections of the same history, because they answer different questions:

  - [State] keeps the canvas as an image, for rendering or for asking what
    colour a pixel is at some moment.
  - [Digest] keeps colour and attribution without an image, for reproducing
    Footer.canvas_checksum - the format's only cross-implementation check.

Neither reads a file. Feed them chunks from package format.

One rule both share, and the easiest thing to get wrong in a new reader: a
pixel's id is y*width+x against *whichever width was in force when its chunk
was written*, not the recording's final width. Apply chunks in order through
one of these and that is handled; decode a whole file against one global width
and every pixel written before the last resize is silently misplaced.
*/
package canvas

import (
	"image"

	ore "github.com/pixelate-it/molten/ore"
)

// State is a canvas replayed into an RGBA image.
type State struct {
	Width, Height uint32
	img           *image.RGBA
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
Resize blanks the canvas at a new size.

Blank is white and fully opaque, which is what a resize keyframe assumes: it
omits every pixel that is still white with nobody's name on it, so the fill has
to match or the omitted pixels come out transparent.
*/
func (s *State) Resize(width, height uint32) {
	s.Width, s.Height = width, height
	s.img = image.NewRGBA(image.Rect(0, 0, int(width), int(height)))

	for i := 0; i < len(s.img.Pix); i += 4 {
		s.img.Pix[i+0] = 0xff
		s.img.Pix[i+1] = 0xff
		s.img.Pix[i+2] = 0xff
		s.img.Pix[i+3] = 0xff
	}
}

func (s *State) applyPixel(p *ore.PixelData) {
	if s.img == nil {
		return
	}
	area := uint64(s.Width) * uint64(s.Height)
	if uint64(p.Id) >= area {
		return
	}

	x := int(p.Id) % int(s.Width)
	y := int(p.Id) / int(s.Width)
	off := s.img.PixOffset(x, y)

	s.img.Pix[off+0] = byte(p.Color >> 16)
	s.img.Pix[off+1] = byte(p.Color >> 8)
	s.img.Pix[off+2] = byte(p.Color)
	s.img.Pix[off+3] = 0xff
}

/*
ApplyKeyframe applies a keyframe, resizing first if it carries a size.

resized is the signal a renderer needs: an encoder cannot change frame size
mid-stream, so a true here is where a new output segment starts.
*/
func (s *State) ApplyKeyframe(kf *ore.Keyframe) (resized bool) {
	if kf.Width != nil && kf.Height != nil {
		s.Resize(*kf.Width, *kf.Height)
		resized = true
	}
	for _, p := range kf.Pixels {
		s.applyPixel(p)
	}
	return resized
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
// outside the canvas or nothing has sized it yet.
func (s *State) ColorAt(x, y int) (uint32, bool) {
	if s.img == nil || x < 0 || y < 0 || x >= int(s.Width) || y >= int(s.Height) {
		return 0, false
	}

	off := s.img.PixOffset(x, y)
	return uint32(s.img.Pix[off+0])<<16 |
		uint32(s.img.Pix[off+1])<<8 |
		uint32(s.img.Pix[off+2]), true
}

// Image returns the live image, not a copy: applying anything else writes
// through it. Copy it before handing it somewhere that outlives the next chunk.
func (s *State) Image() *image.RGBA {
	return s.img
}
