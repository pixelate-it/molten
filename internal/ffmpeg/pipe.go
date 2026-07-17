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

func StartFileEncode(width, height, fps int, outputPath string) (*PipeTarget, error) {
	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", "rawvideo",
		"-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", width, height),
		"-framerate", fmt.Sprintf("%d", fps),
		"-i", "-",
		"-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2",
		"-c:v", "libx264",
		"-preset", "medium",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outputPath,
	)

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
