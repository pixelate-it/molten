package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	ore "github.com/pixelate-it/molten/ore"
	"github.com/pixelate-it/molten/internal/canvas"
	"github.com/pixelate-it/molten/internal/ffmpeg"
	"github.com/pixelate-it/molten/internal/format"
	"github.com/pixelate-it/molten/internal/render"
)

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	inputPath := fs.String("in", "", "path to .mltn file (required)")
	outputPath := fs.String("out", "out.mp4", "output video path")
	fps := fs.Int("fps", 30, "output video fps")
	mode := fs.String("mode", "time", "frame emission mode: time | activity")
	speed := fs.Float64("speed", 3600, "recorded-time speedup factor (mode=time only)")
	step := fs.Int("step", 50, "individual pixel changes per emitted frame (mode=activity only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "render: -in is required")
		return errUsage
	}
	if *mode != "time" && *mode != "activity" {
		fmt.Fprintf(os.Stderr, "render: -mode must be 'time' or 'activity', got %q\n", *mode)
		return errUsage
	}

	f, err := os.Open(*inputPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer f.Close()

	reader := format.NewChunkReader(bufio.NewReaderSize(f, 64*1024));

	header, initialKf, err := format.ReadHeader(reader)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	width, height, ok := format.InitialSize(header, initialKf)
	if !ok {
		return format.ErrMissingInitialKeyframe
	}

	state := canvas.NewState()
	state.Resize(width, height)
	for _, p := range initialKf.Pixels {
		state.ApplySinglePixel(p)
}

	var enc *ffmpeg.PipeTarget
	defer func() {
		if enc != nil {
			_ = enc.Close()
		}
	}()

	emit := func() error {
		if enc == nil {
			e, err := ffmpeg.StartFileEncode(int(state.Width), int(state.Height), *fps, *outputPath)
			if err != nil {
				return fmt.Errorf("start ffmpeg: %w", err)
			}
			enc = e
		}
		img := render.Frame(state)
		if _, err := enc.Stdin.Write(img.Pix); err != nil {
			return fmt.Errorf("write frame: %w", err)
		}
		return nil
	}

	var runErr error
	switch *mode {
	case "time":
		runErr = renderTimeMode(reader, state, initialKf, *speed, *fps, emit)
	case "activity":
		runErr = renderActivityMode(reader, state, initialKf, *step, emit)
	}
	if runErr != nil {
		return runErr
	}

	if enc != nil {
		err := enc.Close()
		enc = nil
		if err != nil {
			return fmt.Errorf("close ffmpeg: %w", err)
		}
	}

	fmt.Printf("Rendered %s (mode=%s)\n", *outputPath, *mode)
	return nil
}

func renderTimeMode(
	reader *format.ChunkReader,
	state *canvas.State,
	initialKf *ore.Keyframe,
	speed float64,
	fps int,
	emit func() error,
) error {
	intervalMs := (1000.0 / float64(fps)) * speed

	var startedAt uint64
	var nextEmitAt float64
	haveStarted := false

	advance := func(ts uint64) error {
		if !haveStarted {
			startedAt = ts
			nextEmitAt = intervalMs
			haveStarted = true
			return emit()
		}
		elapsed := float64(ts - startedAt)
		for elapsed >= nextEmitAt {
			if err := emit(); err != nil {
				return err
			}
			nextEmitAt += intervalMs
		}
		return nil
	}

	if err := advance(initialKf.Timestamp); err != nil {
		return err
	}

	for {
		chunk, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read chunk: %w", err)
		}

		switch {
		case chunk.GetKeyframe() != nil:
			kf := chunk.GetKeyframe()
			state.ApplyKeyframe(kf)
			if err := advance(kf.Timestamp); err != nil {
				return err
			}
		case chunk.GetDelta() != nil:
			d := chunk.GetDelta()
			state.ApplyDelta(d)
			if err := advance(d.Timestamp); err != nil {
				return err
			}
		}
	}
}

func renderActivityMode(
	reader *format.ChunkReader,
	state *canvas.State,
	initialKf *ore.Keyframe,
	step int,
	emit func() error,
) error {
	if step < 1 {
		step = 1
	}

	pixelCount := 0

	applyAndMaybeEmit := func(p *ore.PixelData) error {
		state.ApplySinglePixel(p)
		pixelCount++
		if pixelCount%step == 0 {
			return emit()
		}
		return nil
	}

	if err := emit(); err != nil {
		return err
	}

	for _, p := range initialKf.Pixels {
		if err := applyAndMaybeEmit(p); err != nil {
			return err
		}
	}

	for {
		chunk, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read chunk: %w", err)
		}

		switch {
		case chunk.GetKeyframe() != nil:
			kf := chunk.GetKeyframe()
			if kf.Width != nil && kf.Height != nil {
				state.Resize(*kf.Width, *kf.Height)
			}
			for _, p := range kf.Pixels {
				state.ApplySinglePixel(p)
			}

		case chunk.GetDelta() != nil:
			for _, p := range chunk.GetDelta().Changes {
				if err := applyAndMaybeEmit(p); err != nil {
					return err
				}
			}
		}
	}

	if pixelCount%step != 0 {
		if err := emit(); err != nil {
			return err
		}
	}

	return nil
}
