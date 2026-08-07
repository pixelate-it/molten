package canvas

import (
	"testing"

	ore "github.com/pixelate-it/molten/ore"
)

// Blank is opaque white, and it has to be: a resize keyframe omits every pixel
// still blank, so anything else leaves the omitted ones showing through.
func TestResizeBlanksToOpaqueWhite(t *testing.T) {
	s := NewState()
	s.Resize(3, 2)

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

// Ids are y*width+x, so the width in force is what decides where a pixel lands.
func TestApplyPlacesByIdAgainstTheCurrentWidth(t *testing.T) {
	s := NewState()
	s.Resize(4, 4)
	s.ApplySinglePixel(&ore.PixelData{Id: 6, Color: 0x112233}) // (2,1)

	if colour, ok := s.ColorAt(2, 1); !ok || colour != 0x112233 {
		t.Fatalf("got %06x ok=%v at (2,1)", colour, ok)
	}
	if colour, _ := s.ColorAt(1, 2); colour == 0x112233 {
		t.Error("id 6 on a 4-wide canvas is (2,1), not (1,2)")
	}
}

func TestColorAtRejectsOutOfBounds(t *testing.T) {
	s := NewState()
	s.Resize(2, 2)

	for _, at := range [][2]int{{-1, 0}, {0, -1}, {2, 0}, {0, 2}} {
		if _, ok := s.ColorAt(at[0], at[1]); ok {
			t.Errorf("(%d,%d) is outside a 2x2 canvas", at[0], at[1])
		}
	}
}

// A resize keyframe restates the whole canvas, so it blanks first - anything
// from the old size that it does not restate is gone.
func TestApplyKeyframeResizeBlanksFirst(t *testing.T) {
	s := NewState()
	s.Resize(4, 4)
	s.ApplySinglePixel(&ore.PixelData{Id: 0, Color: 0xff0000})

	w, h := uint32(8), uint32(8)
	if !s.ApplyKeyframe(&ore.Keyframe{Width: &w, Height: &h}) {
		t.Fatal("a keyframe carrying a size is a resize")
	}

	if s.Width != 8 || s.Height != 8 {
		t.Fatalf("got %dx%d, want 8x8", s.Width, s.Height)
	}
	if colour, _ := s.ColorAt(0, 0); colour != BlankColour {
		t.Errorf("got %06x at (0,0), want the resize to have blanked it", colour)
	}
}

// Out-of-range ids are dropped, not panicked on.
func TestApplyIgnoresIdsPastTheCanvas(t *testing.T) {
	s := NewState()
	s.Resize(2, 2)

	s.ApplySinglePixel(&ore.PixelData{Id: 4, Color: 0xff0000}) // one past the end
	s.ApplySinglePixel(&ore.PixelData{Id: 9999, Color: 0xff0000})

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

	s.ApplySinglePixel(&ore.PixelData{Id: 0, Color: 0xff0000})
	s.ApplyDelta(&ore.Delta{Changes: []*ore.PixelData{{Id: 1, Color: 0xff0000}}})

	if s.Image() != nil {
		t.Fatal("no size means no image")
	}
}
