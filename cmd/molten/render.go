package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/pixelate-it/molten/canvas"
	"github.com/pixelate-it/molten/format"
	"github.com/pixelate-it/molten/internal/segment"
	ore "github.com/pixelate-it/molten/ore"
)

// libx264's ceiling on either axis. A render that exceeds it fails inside
// ffmpeg, after every frame has already been piped.
const maxEncodeDimension = 8192

// stringList collects a flag given more than once, so each ffmpeg argument
// arrives as its own token and nothing has to be split on spaces - a filter
// expression is allowed to contain them.
type stringList []string

func (s *stringList) String() string { return fmt.Sprint(*s) }

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	inputPath := fs.String("in", "", "path to a .mltn recording (required)")
	outputPath := fs.String("out", "out.mp4", "output video path (numbered per-segment if the canvas was resized)")
	fps := fs.Int("fps", 30, "output video fps")
	mode := fs.String("mode", "time", "frame emission mode: time | activity")
	speed := fs.Float64("speed", 3600, "recorded-time speedup factor (mode=time only)")
	step := fs.Int("step", 50, "individual pixel changes per emitted frame (mode=activity only)")
	scale := fs.Int("scale", 1, "magnify the output by this whole-number factor, nearest-neighbour (1 = canvas size)")
	var extraArgs stringList
	fs.Var(&extraArgs, "ffmpeg", "extra ffmpeg output argument, repeatable: -ffmpeg -crf -ffmpeg 18")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "render: -in is required")
		return errUsage
	}
	if *scale < 1 {
		fmt.Fprintf(os.Stderr, "render: -scale must be at least 1, got %d\n", *scale)
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

	_, opening, openedAt, err := format.ReadHeader(reader)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	log.Printf("initial canvas size: %dx%d", opening.Width, opening.Height)

	// The opening Resize is what sizes the canvas. It carries no pixels; a
	// season starts blank, and one that does not says so in Deltas.
	state := canvas.NewState()
	state.Reframe(opening)

	/* The opening Resize's own timestamp is where the season's clock starts.
	 * It used to be the opening keyframe's, which was the same instant by a
	 * different name. */

	mgr := segment.NewManager(*outputPath, *fps).
		WithEncodeOptions(*scale, extraArgs)

	/* A canvas can be tiny and the factor is whatever was asked for, so this is
	 * reachable by accident. libx264 refuses beyond 8192 on either axis, and it
	 * refuses at the end of a render rather than the start - which, for a
	 * season that takes minutes to replay, is a long wait for a failure that
	 * was knowable up front. */
	if outW, outH := int(opening.Width)*(*scale), int(opening.Height)*(*scale); outW > maxEncodeDimension || outH > maxEncodeDimension {
		fmt.Fprintf(
			os.Stderr,
			"render: -scale %d gives a %dx%d video, past the %d limit libx264 will encode\n",
			*scale, outW, outH, maxEncodeDimension,
		)
		return errUsage
	}

	if err := mgr.StartSegment(int(opening.Width), int(opening.Height)); err != nil {
		return err
	}
	defer mgr.Close()

	emit := func() error {
		return mgr.WriteFrame(state.Image())
	}

	var runErr error
	switch *mode {
	case "time":
		runErr = renderTimeMode(reader, state, openedAt, *speed, *fps, mgr, emit)
	case "activity":
		runErr = renderActivityMode(reader, state, *step, mgr, emit)
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
	openedAt uint64,
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
		// Unsigned subtraction: a timestamp before the start would wrap to a
		// huge elapsed value and emit frames until the disk filled. Offsets
		// and clock skew both make that reachable, so clamp instead.
		if ts < startedAt {
			return nil
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

	if err := advance(openedAt); err != nil {
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
		case chunk.GetResize() != nil:
			resize := chunk.GetResize()

			/* An encoder cannot change frame size mid-stream, so a resize is
			 * where one output segment ends and the next begins. The canvas
			 * itself is carried across rather than rebuilt - see State.Reframe.
			 */
			state.ApplyResize(resize)

			if err := mgr.StartSegment(int(state.Width), int(state.Height)); err != nil {
				return err
			}
			if err := emit(); err != nil {
				return err
			}
			if err := advance(chunk.Timestamp); err != nil {
				return err
			}

		case chunk.GetDelta() != nil:
			if err := applyDeltaOverTime(
				state, chunk.Timestamp, chunk.GetDelta(), advance,
			); err != nil {
				return err
			}
		}
	}
}

// applyDeltaOverTime plays a delta out across the span it really covers.
//
// A delta carries every pixel changed since the last flush, so applying it
// atomically makes a whole flush window appear in one frame followed by a
// freeze. Sorting by each pixel's offset recovers the true order and lets
// frames be emitted *between* placements, which is what makes a slow render
// look like painting rather than stamping.
//
// Recordings written before the offset field carry none, and take the original
// path exactly - no behaviour change for anything already on disk.
func applyDeltaOverTime(
	state *canvas.State,
	at uint64,
	d *ore.Delta,
	advance func(uint64) error,
) error {
	ordered, timed := format.PlacementOrder(at, d)

	// Nothing to interleave by: keep the original atomic behaviour exactly.
	if !timed {
		state.ApplyDelta(d)
		return advance(at)
	}

	for _, p := range ordered {
		// Frames covering the time before this pixel show the canvas without
		// it, so it appears in the frame after the moment it was painted.
		if err := advance(format.PlacedAt(at, p)); err != nil {
			return err
		}
		state.ApplySinglePixel(p)
	}

	// Then the remainder of the window, up to the flush itself.
	return advance(at)
}

func renderActivityMode(
	reader *format.ChunkReader,
	state *canvas.State,
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

	/* Nothing to lay down first: the opening Resize carries no pixels, and a
	 * season that starts with artwork on it says so in Deltas like everything
	 * else. */
	if err := emit(); err != nil {
		return err
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
		case chunk.GetResize() != nil:
			state.ApplyResize(chunk.GetResize())

			if err := mgr.StartSegment(int(state.Width), int(state.Height)); err != nil {
				return err
			}
			if err := emit(); err != nil {
				return err
			}

		case chunk.GetDelta() != nil:
			/* Recorded order is first-touch order within the flush window, not
			 * placement order - a pixel enters the change set the first time
			 * it is painted and keeps that position however often it is
			 * repainted. Offsets give the real order. */
			ordered, _ := format.PlacementOrder(
				chunk.Timestamp, chunk.GetDelta(),
			)
			for _, p := range ordered {
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
