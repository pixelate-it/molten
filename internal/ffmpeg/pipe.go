package ffmpeg

import (
	"fmt"
	"io"
	"os/exec"
)

type PipeTarget struct {
	Stdin io.WriteCloser
	cmd   *exec.Cmd
}

// Options describes one encode.
//
// Width and Height are the size of the frames written to Stdin - the canvas
// as recorded. Whatever the output ends up being is derived from them here, so
// the caller never has to scale the pixels it pipes.
type Options struct {
	Width, Height int
	FPS           int
	OutputPath    string

	// Scale multiplies the output resolution, nearest-neighbour.
	//
	// A canvas is small - a couple of hundred pixels a side - and a video at
	// that size is not something a player can watch: every viewer scales it,
	// and they all scale it smoothly, which turns pixel art into soup. An
	// integer factor sampled nearest keeps every pixel a hard-edged square,
	// and doing it here rather than in the frame writer keeps the pipe carrying
	// canvas-sized frames however large the video gets.
	//
	// Zero or one leaves the output at canvas size.
	Scale int

	// ExtraArgs are passed to ffmpeg as output options, after the defaults.
	//
	// Later occurrences win in ffmpeg, so these override rather than conflict:
	// -crf, a different -c:v, -preset, even a replacement -vf if the filter
	// chain built here is not what is wanted.
	ExtraArgs []string
}

// VideoSize is the resolution this encode will produce, before the pad to even
// dimensions.
func (o Options) VideoSize() (int, int) {
	scale := max(o.Scale, 1)
	return o.Width * scale, o.Height * scale
}

func (o Options) filterChain() string {
	// Last, and always: yuv420p needs both dimensions even, and an odd canvas
	// (or an odd canvas under an odd scale) is otherwise refused outright.
	chain := "pad=ceil(iw/2)*2:ceil(ih/2)*2"

	if o.Scale > 1 {
		chain = fmt.Sprintf(
			"scale=iw*%d:ih*%d:flags=neighbor,%s", o.Scale, o.Scale, chain,
		)
	}

	return chain
}

func StartFileEncode(o Options) (*PipeTarget, error) {
	args := []string{
		"-y",
		"-f", "rawvideo",
		"-pixel_format", "rgba",
		// The size of what is piped in, which is the canvas - not the output.
		"-video_size", fmt.Sprintf("%dx%d", o.Width, o.Height),
		"-framerate", fmt.Sprintf("%d", o.FPS),
		"-i", "-",
		"-vf", o.filterChain(),
		"-c:v", "libx264",
		"-preset", "medium",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
	}
	args = append(args, o.ExtraArgs...)
	args = append(args, o.OutputPath)

	cmd := exec.Command("ffmpeg", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &PipeTarget{Stdin: stdin, cmd: cmd}, nil
}

func (p *PipeTarget) Close() error {
	if err := p.Stdin.Close(); err != nil {
		return err
	}
	return p.cmd.Wait()
}
