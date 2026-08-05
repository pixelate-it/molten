package segment

import (
	"fmt"
	"image"
	"log"
	"path/filepath"
	"strings"

	"github.com/pixelate-it/molten/internal/ffmpeg"
)

type Manager struct {
	basePath string
	fps      int

	// Applied to every segment, so a canvas that grows mid-recording keeps the
	// same magnification either side of the expansion rather than snapping to a
	// different one halfway through.
	scale     int
	extraArgs []string

	index int
	enc   *ffmpeg.PipeTarget
}

func NewManager(basePath string, fps int) *Manager {
	return &Manager{basePath: basePath, fps: fps, scale: 1}
}

// WithEncodeOptions sets the magnification and the extra ffmpeg arguments used
// for every segment this manager starts.
func (m *Manager) WithEncodeOptions(scale int, extraArgs []string) *Manager {
	m.scale = max(scale, 1)
	m.extraArgs = extraArgs
	return m
}

func (m *Manager) StartSegment(width, height int) error {
	if err := m.closeCurrent(); err != nil {
		return err
	}
	m.index++

	path := m.pathFor(m.index)

	opts := ffmpeg.Options{
		Width:      width,
		Height:     height,
		FPS:        m.fps,
		OutputPath: path,
		Scale:      m.scale,
		ExtraArgs:  m.extraArgs,
	}

	outWidth, outHeight := opts.VideoSize()
	if m.scale > 1 {
		log.Printf(
			"segment %d: starting %dx%d canvas -> %dx%d video (x%d) -> %s",
			m.index, width, height, outWidth, outHeight, m.scale, path,
		)
	} else {
		log.Printf("segment %d: starting %dx%d -> %s", m.index, width, height, path)
	}

	enc, err := ffmpeg.StartFileEncode(opts)
	if err != nil {
		return fmt.Errorf("start segment %d: %w", m.index, err)
	}
	m.enc = enc
	return nil
}

func (m *Manager) WriteFrame(img *image.RGBA) error {
	if m.enc == nil {
		return fmt.Errorf("segment: no active encoder - StartSegment was never called")
	}
	_, err := m.enc.Stdin.Write(img.Pix)
	return err
}

func (m *Manager) Close() error {
	return m.closeCurrent()
}

func (m *Manager) closeCurrent() error {
	if m.enc == nil {
		return nil
	}
	log.Printf("segment %d: finalizing", m.index)
	err := m.enc.Close()
	m.enc = nil
	return err
}

func (m *Manager) pathFor(index int) string {
	if index == 1 {
		return m.basePath
	}
	ext := filepath.Ext(m.basePath)
	stem := strings.TrimSuffix(m.basePath, ext)
	return fmt.Sprintf("%s-%d%s", stem, index, ext)
}
