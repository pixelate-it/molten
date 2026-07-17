package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/pixelate-it/molten/internal/format"
)

func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	inputPath := fs.String("in", "", "path to a recording file, .mltn or legacy .pbr (required)")
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

	header, initialKf, err := format.ReadHeader(reader)
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	width, height, ok := format.InitialSize(header, initialKf)
	if !ok {
		return format.ErrMissingInitialKeyframe
	}

	type resizeEvent struct {
		ts            uint64
		width, height uint32
	}
	resizes := []resizeEvent{{ts: initialKf.Timestamp, width: width, height: height}}

	var keyframes, deltas, chunkCount int
	var lastTs uint64

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
			keyframes++
			lastTs = kf.Timestamp
			if kf.Width != nil && kf.Height != nil {
				resizes = append(resizes, resizeEvent{ts: kf.Timestamp, width: *kf.Width, height: *kf.Height})
			}
		case chunk.GetDelta() != nil:
			deltas++
			lastTs = chunk.GetDelta().Timestamp
		}
	}

	fmt.Printf("Version:       %d\n", header.Version)
	fmt.Printf("Name:          %s\n", header.Name)
	fmt.Printf("Started at:    %d\n", header.StartedAt)
	fmt.Printf("Initial size:  %dx%d\n", width, height)
	fmt.Printf("Keyframes:     %d (excluding initial)\n", keyframes)
	fmt.Printf("Deltas:        %d\n", deltas)
	fmt.Printf("Last ts:       %d\n", lastTs)
	fmt.Printf("Duration:      %.1fs\n", float64(lastTs-header.StartedAt)/1000)

	if len(resizes) > 1 {
		fmt.Printf("Resizes:       %d\n", len(resizes)-1)
		for i, r := range resizes {
			fmt.Printf("  [%d] %dx%d @ ts=%d\n", i+1, r.width, r.height, r.ts)
		}
	}

	return nil
}
