package canvas

import (
	"image"

	ore "github.com/pixelate-it/molten/ore"
)

type State struct {
	Width, Height uint32
	img           *image.RGBA
}

func NewState() *State {
	return &State{}
}

func (s *State) Ready() bool {
	return s.Width > 0 && s.Height > 0
}

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

func (s *State) ApplyKeyframe(kf *ore.Keyframe) {
	if kf.Width != nil && kf.Height != nil {
		s.Resize(*kf.Width, *kf.Height)
	}
	for _, p := range kf.Pixels {
		s.applyPixel(p)
	}
}

func (s *State) ApplyDelta(d *ore.Delta) {
	for _, p := range d.Changes {
		s.applyPixel(p)
	}
}

func (s *State) ApplySinglePixel(p *ore.PixelData) {
	s.applyPixel(p)
}

func (s *State) Image() *image.RGBA {
	return s.img
}
