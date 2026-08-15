package canvas

import (
	"testing"

	"github.com/pixelate-it/molten/format"
	ore "github.com/pixelate-it/molten/ore"
)

// Blank is opaque white, and it has to be: a resize keyframe omits every pixel
// still blank, so anything else leaves the omitted ones showing through.
func TestResizeBlanksToOpaqueWhite(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 3, Height: 2, MinX: 0, MinY: 0})

	if !s.Ready() {
		t.Fatal("a sized canvas is ready")
	}

	for _, at := range [][2]int{{0, 0}, {2, 1}} {
		colour, ok := s.ColorAt(at[0], at[1])
		if !ok || colour != BlankColour {
			t.Errorf("(%d,%d): got %06x ok=%v, want ffffff", at[0], at[1], colour, ok)
		}
	}

	img := s.Image()
	if got := img.Pix[3]; got != 0xff {
		t.Errorf("alpha %d, want 255 - a transparent blank shows through the omitted pixels", got)
	}
}

func TestNewStateIsNotReady(t *testing.T) {
	if NewState().Ready() {
		t.Fatal("nothing has established a size yet")
	}
}

// A pixel names a place on the plane, so where it lands depends on where the
// canvas is - not on how wide it happens to be.
func TestApplyPlacesByCoordinateAgainstTheCurrentCorner(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 4, Height: 4, MinX: -2, MinY: -2})
	s.ApplySinglePixel(&ore.PixelData{X: 0, Y: 0, Color: 0x112233})

	// The origin is two in from the canvas' top-left corner.
	if colour, ok := s.ColorAt(0, 0); !ok || colour != 0x112233 {
		t.Fatalf("got %06x ok=%v at (0,0)", colour, ok)
	}
	if got := s.Image().Pix[s.Image().PixOffset(2, 2)]; got != 0x11 {
		t.Errorf("got %02x in the image at row 2, column 2 - the corner was ignored", got)
	}
}

func TestColorAtRejectsOutOfBounds(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 2, Height: 2, MinX: 0, MinY: 0})

	for _, at := range [][2]int{{-1, 0}, {0, -1}, {2, 0}, {0, 2}} {
		if _, ok := s.ColorAt(at[0], at[1]); ok {
			t.Errorf("(%d,%d) is outside a 2x2 canvas", at[0], at[1])
		}
	}
}

// A Resize moves the window and brings the contents along - it does not blank
// and rebuild, which is what let it stop carrying the canvas.
func TestApplyResizeCarriesTheCanvasAcross(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 4, Height: 4, MinX: 0, MinY: 0})
	s.ApplySinglePixel(&ore.PixelData{X: 0, Y: 0, Color: 0xff0000})

	s.ApplyResize(&ore.Resize{Width: 8, Height: 8, MinX: -2, MinY: -2})

	if s.Width != 8 || s.Height != 8 || s.MinX != -2 || s.MinY != -2 {
		t.Fatalf("got %dx%d at %d,%d, want 8x8 at -2,-2", s.Width, s.Height, s.MinX, s.MinY)
	}

	/* Carried across, not blanked: the pixel is inside the new window, so it
	 * is still there - and it is still at (0, 0), two in from the image's own
	 * corner now. Nothing in the file restated it. */
	if colour, _ := s.ColorAt(0, 0); colour != 0xff0000 {
		t.Errorf("got %06x at (0,0), want the artwork to have survived", colour)
	}
	if colour, _ := s.ColorAt(-2, -2); colour != BlankColour {
		t.Errorf("got %06x at the new corner, want blank", colour)
	}
}

// A cut destroys what falls outside, and the reader has to drop it itself.
func TestStateReframeDropsWhatFallsOutside(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 8, Height: 8})
	s.ApplySinglePixel(&ore.PixelData{X: 1, Y: 1, Color: 0xff0000})
	s.ApplySinglePixel(&ore.PixelData{X: 6, Y: 6, Color: 0x00ff00})

	// Keep the bottom-right quadrant.
	s.Reframe(format.Window{Width: 4, Height: 4, MinX: 4, MinY: 4})

	if _, ok := s.ColorAt(1, 1); ok {
		t.Error("(1,1) is outside the new window and must be gone")
	}
	if colour, ok := s.ColorAt(6, 6); !ok || colour != 0x00ff00 {
		t.Errorf("got %06x ok=%v at (6,6), want the survivor", colour, ok)
	}
}

/* A cut on one axis only. The rows that survive hang off the right, so each
 * has to be *trimmed* - dropping the whole row instead would lose pixels the
 * cut kept, and copying it whole would run past the end of a shorter row. */
func TestReframeTrimsRowsRatherThanDroppingThem(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 4, Height: 4})

	for x := int32(0); x < 4; x++ {
		s.ApplySinglePixel(&ore.PixelData{X: x, Y: 1, Color: 0x112233})
	}

	s.Reframe(format.Window{Width: 2, Height: 4})

	for x := 0; x < 2; x++ {
		if colour, ok := s.ColorAt(x, 1); !ok || colour != 0x112233 {
			t.Errorf("(%d,1): got %06x ok=%v, want the row kept", x, colour, ok)
		}
	}
}

// Coordinates off the canvas are dropped, not panicked on.
func TestApplyIgnoresCoordinatesPastTheCanvas(t *testing.T) {
	s := NewState()
	s.Reframe(format.Window{Width: 2, Height: 2, MinX: 0, MinY: 0})

	// One column past the right edge, which is a valid offset one row down -
	// the case a single check against the area would let through.
	s.ApplySinglePixel(&ore.PixelData{X: 2, Y: 0, Color: 0xff0000})
	s.ApplySinglePixel(&ore.PixelData{X: -1, Y: 0, Color: 0xff0000})
	s.ApplySinglePixel(&ore.PixelData{X: 0, Y: -1, Color: 0xff0000})
	s.ApplySinglePixel(&ore.PixelData{X: 9999, Y: 9999, Color: 0xff0000})

	for y := range 2 {
		for x := range 2 {
			if colour, _ := s.ColorAt(x, y); colour != BlankColour {
				t.Fatalf("(%d,%d) was written by an out-of-range id", x, y)
			}
		}
	}
}

// Applying to a canvas nothing has sized is a no-op rather than a crash: a
// truncated recording can leave a reader here.
func TestApplyBeforeAnySizeIsSafe(t *testing.T) {
	s := NewState()

	s.ApplySinglePixel(&ore.PixelData{X: 0, Y: 0, Color: 0xff0000})
	s.ApplyDelta(&ore.Delta{Changes: []*ore.PixelData{{X: 1, Y: 0, Color: 0xff0000}}})

	if s.Image() != nil {
		t.Fatal("no size means no image")
	}
}
