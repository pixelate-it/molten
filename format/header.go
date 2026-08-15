package format

import (
	"fmt"

	ore "github.com/pixelate-it/molten/ore"
)

/*
Window is the canvas' size and position: how big it is, and which part of the
plane it covers.

A size alone never located a canvas. That was invisible while a pixel was
addressed by an offset *into* the canvas - an offset has nowhere else to point
- and became load-bearing the moment a pixel started naming a place on a plane
that outlives any one canvas size.
*/
type Window struct {
	Width, Height uint32
	MinX, MinY    int32
}

// ReadWindow reads a Resize chunk into a Window. Absent fields are zero, which
// is the right reading for a season that starts on the origin.
func ReadWindow(r *ore.Resize) Window {
	return Window{
		Width:  r.GetWidth(),
		Height: r.GetHeight(),
		MinX:   r.GetMinX(),
		MinY:   r.GetMinY(),
	}
}

/*
FormatVersion is the recording format this build reads.

Bumped whenever the meaning of the bytes changes, and checked - see
ErrUnknownVersion for why that stopped being optional at v6.
*/
const FormatVersion = 10

/*
ReadHeader consumes the two chunks every recording opens with and checks that
it is one: a Header this build knows the version of, then a Resize declaring
the canvas' opening window.

`openedAt` is the opening Resize chunk's own timestamp, which is where the
season's clock starts. It comes back separately because a timestamp lives on
the enclosing chunk now rather than on the payload - see RecordingChunk in
ore/record.proto.

cr is left positioned on the third chunk, so a caller carries straight on with
Next.

A record.v1 file put the size on the Header, whose width/height are retired -
but the version check catches it first and says something more useful about
why. Convert such a file before reading it.
*/
func ReadHeader(
	cr *ChunkReader,
) (header *ore.Header, window Window, openedAt uint64, err error) {
	first, err := cr.Next()
	if err != nil {
		return nil, Window{}, 0, err
	}
	header = first.GetHeader()
	if header == nil {
		return nil, Window{}, 0, ErrNotHeader
	}

	/* Refused rather than attempted. A version this build does not know is a
	 * file whose bytes mean something it has no way to guess, and the failure
	 * mode of guessing is a season rendered wrong rather than a season that
	 * fails to render - the kind nobody notices until they watch the video. */
	if header.GetVersion() != FormatVersion {
		return nil, Window{}, 0, fmt.Errorf(
			"%w: file is v%d, this build reads v%d",
			ErrUnknownVersion, header.GetVersion(), FormatVersion,
		)
	}

	second, err := cr.Next()
	if err != nil {
		return nil, Window{}, 0, err
	}

	opening := second.GetResize()
	if opening == nil {
		return nil, Window{}, 0, ErrMissingInitialResize
	}

	window = ReadWindow(opening)

	// A canvas of no area is not a canvas, and sharp/libx264 both refuse it
	// far from here.
	if window.Width == 0 || window.Height == 0 {
		return nil, Window{}, 0, ErrMissingInitialResize
	}

	return header, window, second.Timestamp, nil
}
