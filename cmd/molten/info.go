package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/cespare/xxhash/v2"
	"github.com/pixelate-it/molten/internal/format"
	ore "github.com/pixelate-it/molten/ore"
)

// Bytes one pixel contributes to the canvas checksum: int32 canonical x,
// int32 canonical y, uint32 colour. See Footer.canvas_checksum in ore/.
const digestRecordSize = 12

// blankColour is what a resize fills a canvas with, and half of what makes a
// pixel too empty to be worth digesting.
const blankColour uint32 = 0xffffff

/*
Just enough of the canvas to reproduce Footer.canvas_checksum.

Not internal/canvas.State: that holds an image, and the checksum is not over an
image. It needs the colour *and* whether anybody's name is on the pixel, since
a white pixel somebody deliberately placed counts and an untouched one does
not - and it needs canonical coordinates, so it has to carry the origin the
keyframes declared.
*/
type canvasDigest struct {
	width, height    uint32
	originX, originY uint32

	colour []uint32
	// Whether an author or a tag is attached, i.e. whether the pixel was
	// placed by somebody rather than left as canvas.
	attributed []bool
}

func (d *canvasDigest) resize(width, height uint32, originX, originY uint32) {
	d.width, d.height = width, height
	d.originX, d.originY = originX, originY

	size := int(width) * int(height)
	d.colour = make([]uint32, size)
	d.attributed = make([]bool, size)

	// A resize starts from a blank canvas, and blank is white - the keyframe
	// that carries it omits every pixel that is still this.
	for i := range d.colour {
		d.colour[i] = blankColour
	}
}

func (d *canvasDigest) apply(p *ore.PixelData) {
	id := int(p.Id)
	if id < 0 || id >= len(d.colour) {
		return
	}

	d.colour[id] = p.Color
	// Assigned, not OR'd: a later placement with no author genuinely clears
	// the attribution, and the writer records it exactly that way.
	d.attributed[id] = p.Author != nil || p.Tag != nil
}

/*
The canvas checksum: xxHash64 (seed 0) over every non-blank pixel in canonical
(y, x) order, each a little-endian int32 x, int32 y, uint32 colour.

Ids are y-major against one width, so ascending id already is ascending
(y, x) - and subtracting a constant origin from both axes cannot reorder them.
That is why there is no sort here.
*/
func (d *canvasDigest) sum() uint64 {
	digest := xxhash.New()

	var record [digestRecordSize]byte

	for id, colour := range d.colour {
		if colour == blankColour && !d.attributed[id] {
			continue
		}

		x := int32(uint32(id)%d.width) - int32(d.originX)
		y := int32(uint32(id)/d.width) - int32(d.originY)

		binary.LittleEndian.PutUint32(record[0:], uint32(x))
		binary.LittleEndian.PutUint32(record[4:], uint32(y))
		binary.LittleEndian.PutUint32(record[8:], colour)

		digest.Write(record[:])
	}

	return digest.Sum64()
}

func keyframeOrigin(kf *ore.Keyframe) (x, y uint32) {
	if kf.OriginX != nil {
		x = *kf.OriginX
	}
	if kf.OriginY != nil {
		y = *kf.OriginY
	}
	return x, y
}

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
		ts               uint64
		width, height    uint32
		originX, originY uint32
	}

	initialOriginX, initialOriginY := keyframeOrigin(initialKf)
	resizes := []resizeEvent{{
		ts:      initialKf.Timestamp,
		width:   width,
		height:  height,
		originX: initialOriginX,
		originY: initialOriginY,
	}}

	digest := &canvasDigest{}
	digest.resize(width, height, initialOriginX, initialOriginY)
	for _, p := range initialKf.Pixels {
		digest.apply(p)
	}

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
		case chunk.GetKeyframe() != nil:
			kf := chunk.GetKeyframe()
			keyframes++
			lastTs = kf.Timestamp

			if kf.Width != nil && kf.Height != nil {
				originX, originY := keyframeOrigin(kf)

				resizes = append(resizes, resizeEvent{
					ts:      kf.Timestamp,
					width:   *kf.Width,
					height:  *kf.Height,
					originX: originX,
					originY: originY,
				})

				digest.resize(*kf.Width, *kf.Height, originX, originY)
			}

			for _, p := range kf.Pixels {
				digest.apply(p)
			}

		case chunk.GetDelta() != nil:
			delta := chunk.GetDelta()
			deltas++
			lastTs = delta.Timestamp
			placed += uint64(len(delta.Changes))

			for _, p := range delta.Changes {
				digest.apply(p)
			}

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
	fmt.Printf("Initial size:  %dx%d\n", width, height)
	fmt.Printf("Current size:  %dx%d\n", digest.width, digest.height)
	fmt.Printf("Origin:        %d,%d\n", digest.originX, digest.originY)
	fmt.Printf("Keyframes:     %d (excluding initial)\n", keyframes)
	fmt.Printf("Deltas:        %d\n", deltas)
	fmt.Printf("Pixel changes: %d\n", placed)
	fmt.Printf("Last ts:       %d\n", lastTs)
	fmt.Printf("Duration:      %.1fs\n", float64(lastTs-header.StartedAt)/1000)

	if len(resizes) > 1 {
		fmt.Printf("Resizes:       %d\n", len(resizes)-1)
		for i, r := range resizes {
			fmt.Printf("  [%d] %dx%d origin %d,%d @ ts=%d\n",
				i+1, r.width, r.height, r.originX, r.originY, r.ts)
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
func printSeal(footer *ore.Footer, digest *canvasDigest, placed uint64, afterFooter int) {
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

	sum := digest.sum()

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
