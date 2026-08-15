package canvas

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
	"github.com/pixelate-it/molten/format"
	ore "github.com/pixelate-it/molten/ore"
)

// Bytes one pixel contributes to the checksum: int32 x, int32 y, uint32
// colour. See Footer.canvas_checksum in ore/.
const digestRecordSize = 12

// BlankColour is what a resize fills a canvas with, and half of what makes a
// pixel too empty to be worth digesting.
const BlankColour uint32 = 0xffffff

/*
Digest replays a recording into Footer.canvas_checksum.

Deliberately not [State]: that holds an image, and the checksum is not over an
image. It needs the colour *and* whether anybody's name is on the pixel - a
white pixel somebody placed counts, an untouched one does not.

This is the only cross-implementation check the format has. A mismatch means
this reader and whatever wrote the file disagree about what the file means,
which is worth finding out from a one-second command rather than from a
rendered video months later.
*/
type Digest struct {
	width, height uint32
	// The canvas' top-left corner, so a coordinate can be turned into a slot
	// in the flat slices below. Not part of what is hashed.
	minX, minY int32

	colour []uint32
	// Whether an author or a tag is attached, i.e. whether the pixel was
	// placed by somebody rather than left as canvas.
	attributed []bool
}

// NewDigest returns a digest with no size yet; the recording's first keyframe
// establishes one.
func NewDigest() *Digest {
	return &Digest{}
}

// Size returns the canvas size as it stands, and Corner where it sits. Both
// move with every resize keyframe applied.
func (d *Digest) Size() (width, height uint32) { return d.width, d.height }

// Corner returns the canvas' top-left corner as the last resize declared it.
func (d *Digest) Corner() (minX, minY int32) { return d.minX, d.minY }

/*
Reframe moves the digest onto a new window, keeping every pixel still inside
it. The mirror of [State.Reframe]; see the note there for why a Resize carries
no pixels.
*/
func (d *Digest) Reframe(w format.Window) {
	size := int(w.Width) * int(w.Height)

	colour := make([]uint32, size)
	attributed := make([]bool, size)

	// Blank is white - a pixel is only artwork if somebody put it there.
	for i := range colour {
		colour[i] = BlankColour
	}

	shiftX := int(d.minX - w.MinX)
	shiftY := int(d.minY - w.MinY)

	for row := 0; row < int(d.height); row++ {
		movedRow := row + shiftY
		if movedRow < 0 || movedRow >= int(w.Height) {
			continue
		}

		for column := 0; column < int(d.width); column++ {
			movedColumn := column + shiftX
			// Per axis - see the note in State.applyPixel.
			if movedColumn < 0 || movedColumn >= int(w.Width) {
				continue
			}

			from := row*int(d.width) + column
			to := movedRow*int(w.Width) + movedColumn

			colour[to] = d.colour[from]
			attributed[to] = d.attributed[from]
		}
	}

	d.width, d.height = w.Width, w.Height
	d.minX, d.minY = w.MinX, w.MinY
	d.colour, d.attributed = colour, attributed
}

// ApplyResize applies a Resize chunk.
func (d *Digest) ApplyResize(r *ore.Resize) {
	d.Reframe(format.ReadWindow(r))
}

// ApplyKeyframe applies a restatement of contents. Carries no geometry - see
// [State.ApplyKeyframe].
func (d *Digest) ApplyKeyframe(kf *ore.Keyframe) {
	for _, p := range kf.Pixels {
		d.Apply(p)
	}
}

// ApplyDelta applies every change in a delta.
func (d *Digest) ApplyDelta(delta *ore.Delta) {
	for _, p := range delta.Changes {
		d.Apply(p)
	}
}

// Apply applies one pixel.
func (d *Digest) Apply(p *ore.PixelData) {
	// Per axis - see the same note in State.applyPixel.
	col := int(p.X - d.minX)
	row := int(p.Y - d.minY)
	if col < 0 || row < 0 || col >= int(d.width) || row >= int(d.height) {
		return
	}

	cell := row*int(d.width) + col

	d.colour[cell] = p.Color
	// Assigned, not OR'd: a later placement with no author genuinely clears
	// the attribution, and the writer records it exactly that way.
	d.attributed[cell] = p.Author != nil || p.Tag != nil
}

/*
Sum returns the canvas checksum: xxHash64 (seed 0) over every non-blank pixel
in (y, x) order, each a little-endian int32 x, int32 y, uint32 colour.

The slices are laid out row by row, so walking them in order already is
ascending (y, x) - and adding the canvas' corner to both axes cannot reorder
them. That is why there is no sort here.

The coordinates hashed are the pixels' own, which is what makes the same
artwork hash the same either side of a resize.

It is not cryptographic and is not trying to be. It catches a second
implementation that decoded the file differently; it does nothing against
someone who can rewrite the footer along with the chunks.
*/
func (d *Digest) Sum() uint64 {
	digest := xxhash.New()

	var record [digestRecordSize]byte

	for cell, colour := range d.colour {
		if colour == BlankColour && !d.attributed[cell] {
			continue
		}

		x := d.minX + int32(uint32(cell)%d.width)
		y := d.minY + int32(uint32(cell)/d.width)

		binary.LittleEndian.PutUint32(record[0:], uint32(x))
		binary.LittleEndian.PutUint32(record[4:], uint32(y))
		binary.LittleEndian.PutUint32(record[8:], colour)

		digest.Write(record[:])
	}

	return digest.Sum64()
}
