package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/pixelate-it/molten/internal/canvas"
	"github.com/pixelate-it/molten/internal/format"
	"github.com/pixelate-it/molten/internal/render"
	"github.com/pixelate-it/molten/internal/segment"
	ore "github.com/pixelate-it/molten/ore"
)

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	inputPath := fs.String("in", "", "path to a recording file, .mltn or legacy .pbr (required)")
	outputPath := fs.String("out", "out.mp4", "output video path (numbered per-segment if the canvas was resized)")
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

	log.Printf("opening %s", *inputPath)

	f, err := os.Open(*inputPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer f.Close()

	reader := format.NewChunkReader(bufio.NewReaderSize(f, 64*1024))

	header, initialKf, err := format.ReadHeader(reader)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	width, height, ok := format.InitialSize(header, initialKf)
	if !ok {
		return format.ErrMissingInitialKeyframe
	}
	log.Printf("initial canvas size: %dx%d", width, height)

	state := canvas.NewState()
	state.Resize(width, height)
	for _, p := range initialKf.Pixels {
		state.ApplySinglePixel(p)
	}

	mgr := segment.NewManager(*outputPath, *fps)
	if err := mgr.StartSegment(int(width), int(height)); err != nil {
		return err
	}
	defer mgr.Close()

	emit := func() error {
		return mgr.WriteFrame(render.Frame(state))
	}

	var runErr error
	switch *mode {
	case "time":
		runErr = renderTimeMode(reader, state, initialKf, *speed, *fps, mgr, emit)
	case "activity":
		runErr = renderActivityMode(reader, state, initialKf, *step, mgr, emit)
	}
	if runErr != nil {
		return runErr
	}

	if err := mgr.Close(); err != nil {
		return fmt.Errorf("close last segment: %w", err)
	}

	log.Printf("done")
	return nil
}

func renderTimeMode(
	reader *format.ChunkReader,
	state *canvas.State,
	initialKf *ore.Keyframe,
	speed float64,
	fps int,
	mgr *segment.Manager,
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

	chunkCount := 0
	for {
		chunk, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read chunk: %w", err)
		}
		chunkCount++
		if chunkCount%2000 == 0 {
			log.Printf("processed %d chunks...", chunkCount)
		}

		switch {
		case chunk.GetKeyframe() != nil:
			kf := chunk.GetKeyframe()
			resized := state.ApplyKeyframe(kf)
			if resized {
				if err := mgr.StartSegment(int(state.Width), int(state.Height)); err != nil {
					return err
				}
				if err := emit(); err != nil {
					return err
				}
			}
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
	mgr *segment.Manager,
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

	chunkCount := 0
	for {
		chunk, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read chunk: %w", err)
		}
		chunkCount++
		if chunkCount%2000 == 0 {
			log.Printf("processed %d chunks...", chunkCount)
		}

		switch {
		case chunk.GetKeyframe() != nil:
			kf := chunk.GetKeyframe()
			resized := state.ApplyKeyframe(kf)
			if resized {
				if err := mgr.StartSegment(int(state.Width), int(state.Height)); err != nil {
					return err
				}
				if err := emit(); err != nil {
					return err
				}
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
