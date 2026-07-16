package render

import (
	"image"

	"github.com/pixelate-it/molten/internal/canvas"
)

func Frame(s *canvas.State) *image.RGBA {
	return s.Image()
}
