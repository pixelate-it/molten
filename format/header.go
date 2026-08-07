package format

import ore "github.com/pixelate-it/molten/ore"

/*
CanvasSize reads the canvas size off a keyframe.

The size lives on a Keyframe and nowhere else, because it can change
mid-recording and a Keyframe is the only chunk that can say so. record.v1 put
it on the Header instead; that field is retired and those files no longer read
(see the reserved fields in ore/record.proto).

ok is false when the keyframe does not carry one - which for a recording's
opening keyframe is a file nothing can decode, since pixel ids are y*width+x
and without a width there is nowhere to put a pixel.
*/
func CanvasSize(kf *ore.Keyframe) (width, height uint32, ok bool) {
	if kf.Width == nil || kf.Height == nil {
		return 0, 0, false
	}
	return *kf.Width, *kf.Height, true
}

/*
ReadHeader consumes the two chunks every recording opens with and checks that
it is one: a Header, then a Keyframe carrying the canvas size.

width and height come back because reading them is the only reason the second
chunk is mandatory; the keyframe itself comes back too because it is not just a
size announcement - it carries the opening state of the canvas, and its pixels
still have to be applied.

cr is left positioned on the third chunk, so a caller carries straight on with
Next.

A record.v1 file fails here with ErrMissingInitialKeyframe, and that is the
intended outcome rather than an accident: it put the size on the Header, that
field is retired, and there is nothing left to read a size from. Convert such a
file before reading it.
*/
func ReadHeader(cr *ChunkReader) (header *ore.Header, kf *ore.Keyframe, width, height uint32, err error) {
	first, err := cr.Next()
	if err != nil {
		return nil, nil, 0, 0, err
	}
	header = first.GetHeader()
	if header == nil {
		return nil, nil, 0, 0, ErrNotHeader
	}

	second, err := cr.Next()
	if err != nil {
		return nil, nil, 0, 0, err
	}
	kf = second.GetKeyframe()
	if kf == nil {
		return nil, nil, 0, 0, ErrMissingInitialKeyframe
	}

	width, height, ok := CanvasSize(kf)
	if !ok {
		return nil, nil, 0, 0, ErrMissingInitialKeyframe
	}

	return header, kf, width, height, nil
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
