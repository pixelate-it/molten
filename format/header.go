package format

import ore "github.com/pixelate-it/molten/ore"

/*
InitialSize resolves the canvas size a recording starts at.

The size lives on the first Keyframe, not on the Header, because it can change
mid-recording. Legacy .pbr (record.v1) files put it on the Header instead, and
that is preferred where present purely so those files keep reading.

ok is false when neither carries it, which is a file nothing can decode: pixel
ids are y*width+x, so without a width there is no way to place a single pixel.
*/
func InitialSize(header *ore.Header, firstKeyframe *ore.Keyframe) (width, height uint32, ok bool) {
	if header.Width != nil && header.Height != nil {
		return *header.Width, *header.Height, true
	}
	if firstKeyframe.Width != nil && firstKeyframe.Height != nil {
		return *firstKeyframe.Width, *firstKeyframe.Height, true
	}
	return 0, 0, false
}

/*
ReadHeader consumes the two chunks every recording opens with and checks that
it is one: a Header, then a Keyframe that establishes the canvas size.

cr is left positioned on the third chunk, so a caller carries straight on with
Next. The keyframe comes back as well as the header because it is not just a
size announcement - it carries the opening state of the canvas, and its pixels
still have to be applied.
*/
func ReadHeader(cr *ChunkReader) (*ore.Header, *ore.Keyframe, error) {
	first, err := cr.Next()
	if err != nil {
		return nil, nil, err
	}
	header := first.GetHeader()
	if header == nil {
		return nil, nil, ErrNotHeader
	}

	second, err := cr.Next()
	if err != nil {
		return nil, nil, err
	}
	kf := second.GetKeyframe()
	if kf == nil {
		return nil, nil, ErrMissingInitialKeyframe
	}

	if _, _, ok := InitialSize(header, kf); !ok {
		return nil, nil, ErrMissingInitialKeyframe
	}

	return header, kf, nil
}

/*
KeyframeOrigin reads the accumulated anchor offset off a keyframe.

Absent means zero, which is right for an opening keyframe and is the only
answer available for a recording written before the field existed - such a
recording either never resized, or resized in a way nothing can now reconstruct
(the anchor itself is never recorded, only its result).

The origin converts between the buffer's addressing and the season's original
one, and it is why a permalink or a cross-implementation checksum survives an
expansion:

	canonical = local - origin
	local     = canonical + origin

The origin is never negative. A canonical coordinate absolutely can be -
anything painted into space an expansion added on the left or the top.
*/
func KeyframeOrigin(kf *ore.Keyframe) (x, y uint32) {
	if kf.OriginX != nil {
		x = *kf.OriginX
	}
	if kf.OriginY != nil {
		y = *kf.OriginY
	}
	return x, y
}

// IsResize reports whether a keyframe carries a canvas size, which is the
// format's only resize signal. A reader that sees one must blank its canvas at
// the new size before applying the keyframe's pixels, because a resize keyframe
// restates everything that survives.
func IsResize(kf *ore.Keyframe) bool {
	return kf.Width != nil && kf.Height != nil
}
