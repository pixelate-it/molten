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
	ore "github.com/pixelate-it/molten/ore"
)

func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	inputPath := fs.String("in", "", "path to a .mltn recording (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "info: -in is required")
		return errUsage
	}

	log.Printf("opening %s", *inputPath)

	f, err := os.Open(*inputPath)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer f.Close()

	reader := format.NewChunkReader(bufio.NewReaderSize(f, 64*1024))

	header, first, err := format.ReadHeader(reader)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	opening := format.ReadWindow(first)

	type resizeEvent struct {
		ts     uint64
		window format.Window
	}

	resizes := []resizeEvent{{ts: first.Timestamp, window: opening}}

	digest := canvas.NewDigest()
	digest.Reframe(opening)

	var keyframes, deltas, chunkCount int
	// Pixel changes across deltas, which is what Footer.total_pixels_placed
	// counts - keyframes restate pixels their delta already counted.
	var placed uint64
	var lastTs uint64

	var footer *ore.Footer
	// Chunks after the one that claimed to be last. A sealed recording has
	// none, and anything here means something wrote past the seal.
	var afterFooter int

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

		/* Everything past the footer is counted and otherwise ignored -
		 * applying it would digest a canvas the checksum was never taken of,
		 * and report a mismatch that says nothing about either side. */
		if footer != nil {
			afterFooter++
			continue
		}

		switch {
		case chunk.GetResize() != nil:
			resize := chunk.GetResize()
			lastTs = resize.Timestamp

			resizes = append(resizes, resizeEvent{
				ts:     resize.Timestamp,
				window: format.ReadWindow(resize),
			})

			digest.ApplyResize(resize)

		case chunk.GetKeyframe() != nil:
			kf := chunk.GetKeyframe()
			keyframes++
			lastTs = kf.Timestamp

			digest.ApplyKeyframe(kf)

		case chunk.GetDelta() != nil:
			delta := chunk.GetDelta()
			deltas++
			lastTs = delta.Timestamp
			placed += uint64(len(delta.Changes))

			digest.ApplyDelta(delta)

		case chunk.GetFooter() != nil:
			footer = chunk.GetFooter()
			lastTs = footer.Timestamp
		}
	}

	fmt.Printf("Version:       %d\n", header.Version)
	fmt.Printf("Name:          %s\n", header.Name)
	fmt.Printf("Started at:    %d\n", header.StartedAt)
	if header.GameId != nil {
		fmt.Printf("Game:          %d\n", *header.GameId)
	}
	if header.Cooldown != nil {
		fmt.Printf("Cooldown:      %dms\n", *header.Cooldown)
	}
	if header.EndsAt != nil {
		fmt.Printf("Scheduled end: %d\n", *header.EndsAt)
	}
	currentWidth, currentHeight := digest.Size()
	minX, minY := digest.Corner()

	fmt.Printf("Initial size:  %dx%d\n", opening.Width, opening.Height)
	fmt.Printf("Current size:  %dx%d\n", currentWidth, currentHeight)
	fmt.Printf("Corner:        %d,%d\n", minX, minY)
	fmt.Printf("Keyframes:     %d\n", keyframes)
	fmt.Printf("Deltas:        %d\n", deltas)
	fmt.Printf("Pixel changes: %d\n", placed)
	fmt.Printf("Last ts:       %d\n", lastTs)
	/* Unsigned subtraction, so a recording whose last chunk predates its own
	 * header wraps to nonsense - which a freshly rolled file does by default,
	 * having no chunk after the opening keyframe to take a time from. */
	if lastTs >= header.StartedAt {
		fmt.Printf("Duration:      %.1fs\n", float64(lastTs-header.StartedAt)/1000)
	} else {
		fmt.Printf("Duration:      unknown (last chunk predates the header)\n")
	}

	if len(resizes) > 1 {
		fmt.Printf("Resizes:       %d\n", len(resizes)-1)
		for i, r := range resizes {
			fmt.Printf("  [%d] %dx%d at %d,%d @ ts=%d\n",
				i+1, r.window.Width, r.window.Height,
				r.window.MinX, r.window.MinY, r.ts)
		}
	}

	printSeal(footer, digest, placed, afterFooter)

	return nil
}

/*
Reports whether the recording is finished, and whether this reader agrees with
the writer about what it says.

The footer is the only thing that distinguishes a completed recording from one
whose writer was killed - both simply stop after a valid chunk - and its two
counters are the only cross-implementation check the format has. A mismatch
here means Go and the TypeScript writer disagree about what the file means,
which is worth finding out from a one-second command rather than from a
rendered video months later.
*/
func printSeal(footer *ore.Footer, digest *canvas.Digest, placed uint64, afterFooter int) {
	if footer == nil {
		fmt.Printf("Sealed:        no (still being written, or the writer was interrupted)\n")
		return
	}

	fmt.Printf("Sealed:        yes @ ts=%d\n", footer.Timestamp)

	fmt.Printf("  pixels:      %d recorded", footer.TotalPixelsPlaced)
	if footer.TotalPixelsPlaced == placed {
		fmt.Printf(", matches\n")
	} else {
		fmt.Printf(", MISMATCH - %d read from deltas\n", placed)
	}

	sum := digest.Sum()

	fmt.Printf("  checksum:    %016x recorded", footer.CanvasChecksum)
	if footer.CanvasChecksum == sum {
		fmt.Printf(", matches\n")
	} else {
		fmt.Printf(", MISMATCH - %016x replayed\n", sum)
	}

	if afterFooter > 0 {
		fmt.Printf("  WARNING:     %d chunk(s) written after the footer\n", afterFooter)
	}
}
