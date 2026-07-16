package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/pixelate-it/molten/internal/format"
)

func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	inputPath := fs.String("in", "", "path to .mltn file (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "info: -in is required")
		return errUsage
	}

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

	fmt.Printf("Version:       %d\n", header.Version)
	fmt.Printf("Name:          %s\n", header.Name)
	fmt.Printf("Started at:    %d\n", header.StartedAt)
	fmt.Printf("Initial size:  %dx%d\n", width, height)

	var keyframes, deltas int
	var lastTs uint64

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
			keyframes++
			lastTs = chunk.GetKeyframe().Timestamp
		case chunk.GetDelta() != nil:
			deltas++
			lastTs = chunk.GetDelta().Timestamp
		}
	}

	fmt.Printf("Keyframes:     %d (excluding initial)\n", keyframes)
	fmt.Printf("Deltas:        %d\n", deltas)
	fmt.Printf("Last ts:       %d\n", lastTs)
	fmt.Printf("Duration:      %.1fs\n", float64(lastTs-header.StartedAt)/1000)

	return nil
}
