package format

import (
	"fmt"

	ore "github.com/pixelate-it/molten/ore"
)

/*
CanvasSize reads the canvas size off a keyframe.

The size lives on a Keyframe and nowhere else, because it can change
mid-recording and a Keyframe is the only chunk that can say so. record.v1 put
it on the Header instead; that field is retired and those files no longer read
(see the reserved fields in ore/record.proto).

ok is false when the keyframe does not carry one - which for a recording's
opening keyframe is a file nothing can decode: a pixel names a coordinate, and
without a size and a corner there is no saying which of them the canvas covers.
*/
func CanvasSize(kf *ore.Keyframe) (width, height uint32, ok bool) {
	if kf.Width == nil || kf.Height == nil {
		return 0, 0, false
	}
	return *kf.Width, *kf.Height, true
}

/*
FormatVersion is the recording format this build reads.

Bumped whenever the meaning of the bytes changes, and checked - see
ErrUnknownVersion for why that stopped being optional at v6.
*/
const FormatVersion = 6

/*
ReadHeader consumes the two chunks every recording opens with and checks that
it is one: a Header this build knows the version of, then a Keyframe carrying
the canvas size.

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

	/* Refused rather than attempted. A version this build does not know is a
	 * file whose PixelData means something it has no way to guess, and the
	 * failure mode of guessing is a season rendered wrong rather than a
	 * season that fails to render - the kind nobody notices until they watch
	 * the video. */
	if header.GetVersion() != FormatVersion {
		return nil, nil, 0, 0, fmt.Errorf(
			"%w: file is v%d, this build reads v%d",
			ErrUnknownVersion, header.GetVersion(), FormatVersion,
		)
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
KeyframeCorner reads the canvas' top-left corner off a keyframe, in the same
coordinates its pixels are named by.

A size says how big the canvas is; this says where it is. Both are needed and
neither implies the other, because a keyframe is sparse - it skips every white
unattributed pixel - so the pixels it carries cannot be used to work out the
region they belong to.

Absent means zero, which is right for a season's opening keyframe: the origin
is defined as the corner the canvas started on. It is also the only answer
available for a recording written before the field existed, and a correct one,
since nothing before it could express a canvas that had moved.

Negative once a season has been expanded leftwards or upwards - the corner is a
coordinate, not a distance.
*/
func KeyframeCorner(kf *ore.Keyframe) (minX, minY int32) {
	if kf.MinX != nil {
		minX = *kf.MinX
	}
	if kf.MinY != nil {
		minY = *kf.MinY
	}
	return minX, minY
}

// IsResize reports whether a keyframe carries a canvas size, which is the
// format's only resize signal. A reader that sees one must blank its canvas at
// the new size before applying the keyframe's pixels, because a resize keyframe
// restates everything that survives.
func IsResize(kf *ore.Keyframe) bool {
	return kf.Width != nil && kf.Height != nil
}
