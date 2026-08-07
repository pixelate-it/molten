package canvas

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
	"github.com/pixelate-it/molten/format"
	ore "github.com/pixelate-it/molten/ore"
)

// Bytes one pixel contributes to the checksum: int32 canonical x, int32
// canonical y, uint32 colour. See Footer.canvas_checksum in ore/.
const digestRecordSize = 12

// BlankColour is what a resize fills a canvas with, and half of what makes a
// pixel too empty to be worth digesting.
const BlankColour uint32 = 0xffffff

/*
Digest replays a recording into Footer.canvas_checksum.

Deliberately not [State]: that holds an image, and the checksum is not over an
image. It needs the colour *and* whether anybody's name is on the pixel - a
white pixel somebody placed counts, an untouched one does not - and it needs
canonical coordinates, so it has to carry the origin the keyframes declared.

This is the only cross-implementation check the format has. A mismatch means
this reader and whatever wrote the file disagree about what the file means,
which is worth finding out from a one-second command rather than from a
rendered video months later.
*/
type Digest struct {
	width, height    uint32
	originX, originY uint32

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

// Size returns the canvas size as it stands, and Origin the accumulated anchor
// offset. Both move with every resize keyframe applied.
func (d *Digest) Size() (width, height uint32) { return d.width, d.height }

// Origin returns the accumulated anchor offset the last resize declared.
func (d *Digest) Origin() (x, y uint32) { return d.originX, d.originY }

// Resize blanks the digest at a new size and adopts a new origin.
func (d *Digest) Resize(width, height, originX, originY uint32) {
	d.width, d.height = width, height
	d.originX, d.originY = originX, originY

	size := int(width) * int(height)
	d.colour = make([]uint32, size)
	d.attributed = make([]bool, size)

	// A resize starts from a blank canvas, and blank is white - the keyframe
	// that carries it omits every pixel that is still this.
	for i := range d.colour {
		d.colour[i] = BlankColour
	}
}

// ApplyKeyframe applies a keyframe, resizing and adopting its origin first if
// it carries a size. resized mirrors [State.ApplyKeyframe].
func (d *Digest) ApplyKeyframe(kf *ore.Keyframe) (resized bool) {
	if format.IsResize(kf) {
		originX, originY := format.KeyframeOrigin(kf)
		d.Resize(*kf.Width, *kf.Height, originX, originY)
		resized = true
	}
	for _, p := range kf.Pixels {
		d.Apply(p)
	}
	return resized
}

// ApplyDelta applies every change in a delta.
func (d *Digest) ApplyDelta(delta *ore.Delta) {
	for _, p := range delta.Changes {
		d.Apply(p)
	}
}

// Apply applies one pixel.
func (d *Digest) Apply(p *ore.PixelData) {
	id := int(p.Id)
	if id < 0 || id >= len(d.colour) {
		return
	}

	d.colour[id] = p.Color
	// Assigned, not OR'd: a later placement with no author genuinely clears
	// the attribution, and the writer records it exactly that way.
	d.attributed[id] = p.Author != nil || p.Tag != nil
}

/*
Sum returns the canvas checksum: xxHash64 (seed 0) over every non-blank pixel
in canonical (y, x) order, each a little-endian int32 x, int32 y, uint32 colour.

Ids are y-major against one width, so ascending id already is ascending (y, x) -
and subtracting a constant origin from both axes cannot reorder them. That is
why there is no sort here.

Canonical rather than raw ids, or the same physical artwork would hash
differently either side of a resize purely because the width changed.

It is not cryptographic and is not trying to be. It catches a second
implementation that decoded the file differently; it does nothing against
someone who can rewrite the footer along with the chunks.
*/
func (d *Digest) Sum() uint64 {
	digest := xxhash.New()

	var record [digestRecordSize]byte

	for id, colour := range d.colour {
		if colour == BlankColour && !d.attributed[id] {
			continue
		}

		x := int32(uint32(id)%d.width) - int32(d.originX)
		y := int32(uint32(id)/d.width) - int32(d.originY)

		binary.LittleEndian.PutUint32(record[0:], uint32(x))
		binary.LittleEndian.PutUint32(record[4:], uint32(y))
		binary.LittleEndian.PutUint32(record[8:], colour)

		digest.Write(record[:])
	}

	return digest.Sum64()
}
