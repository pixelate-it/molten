package format

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	ore "github.com/pixelate-it/molten/ore"
)

// One pixel per column of row 0 - nothing here cares where they are.
func delta(ts uint64, xs ...int32) *ore.RecordingChunk {
	changes := make([]*ore.PixelData, len(xs))
	for i, x := range xs {
		changes[i] = &ore.PixelData{X: x, Y: 0, Color: 0x112233}
	}
	return &ore.RecordingChunk{
		Timestamp: ts,
		Payload:   &ore.RecordingChunk_Delta{Delta: &ore.Delta{Changes: changes}},
	}
}

// encoded returns the wire bytes of the given chunks, plus the offset each one
// ends at, so a test can cut the stream anywhere.
func encoded(t *testing.T, chunks ...*ore.RecordingChunk) ([]byte, []int) {
	t.Helper()

	var buf bytes.Buffer
	w := NewChunkWriter(&buf)
	ends := make([]int, 0, len(chunks))

	for _, c := range chunks {
		if err := w.Write(c); err != nil {
			t.Fatalf("write chunk: %v", err)
		}
		ends = append(ends, buf.Len())
	}

	return buf.Bytes(), ends
}

func TestChunkReaderRoundTrip(t *testing.T) {
	data, _ := encoded(t, delta(1000, 1, 2), delta(2000, 3))

	cr := NewChunkReader(bufio.NewReader(bytes.NewReader(data)))

	for _, want := range []uint64{1000, 2000} {
		chunk, err := cr.Next()
		if err != nil {
			t.Fatalf("ts %d: %v", want, err)
		}
		if got := chunk.Timestamp; got != want {
			t.Fatalf("got ts %d, want %d", got, want)
		}
	}

	if _, err := cr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last chunk: got %v, want io.EOF", err)
	}
}

func TestChunkReaderRejectsCorruptLength(t *testing.T) {
	// A length of zero is not a zero-length chunk, it is a broken file.
	cr := NewChunkReader(bytes.NewReader([]byte{0, 0, 0, 0}))

	if _, err := cr.Next(); !errors.Is(err, ErrCorruptChunkLength) {
		t.Fatalf("got %v, want ErrCorruptChunkLength", err)
	}
}

/*
The reason TailReader exists.

A live consumer polls, so it meets the writer mid-chunk. ChunkReader consumes
the length prefix and whatever body had been flushed before reporting io.EOF,
which leaves it positioned inside a chunk body - so the next four bytes it
reads are body, taken for a length.
*/
func TestChunkReaderConsumesAPartialChunk(t *testing.T) {
	data, ends := encoded(t, delta(1000, 1), delta(2000, 2))
	partial := data[:ends[0]+6] // all of chunk 1, part of chunk 2

	cr := NewChunkReader(bufio.NewReader(bytes.NewReader(partial)))

	if _, err := cr.Next(); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if _, err := cr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("half-written chunk: got %v, want io.EOF", err)
	}

	// The bytes are gone: resuming from where it stopped reads body as length.
	rest := NewChunkReader(bytes.NewReader(data[ends[0]+6:]))
	if _, err := rest.Next(); err == nil {
		t.Fatal("expected the resumed stream to be desynchronised, but it parsed")
	}
}

// The same situation, survived: the reader rewinds to the start of the
// incomplete chunk, so the bytes already flushed are read again with the rest.
func TestTailReaderResumesAcrossAPartialChunk(t *testing.T) {
	data, ends := encoded(t, delta(1000, 1), delta(2000, 2), delta(3000, 3))

	path := filepath.Join(t.TempDir(), "live.mltn")
	if err := os.WriteFile(path, data[:ends[0]+6], 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tr := NewTailReader(f)

	chunk, err := tr.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if got := chunk.Timestamp; got != 1000 {
		t.Fatalf("got ts %d, want 1000", got)
	}

	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("half-written chunk: got %v, want io.EOF", err)
	}

	// The offset must be back at the start of the chunk that is not all there,
	// not somewhere inside it.
	off, err := tr.Offset()
	if err != nil {
		t.Fatal(err)
	}
	if off != int64(ends[0]) {
		t.Fatalf("rewound to %d, want %d - the start of the incomplete chunk", off, ends[0])
	}

	// the writer finishes the file
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, want := range []uint64{2000, 3000} {
		chunk, err := tr.Next()
		if err != nil {
			t.Fatalf("ts %d after the write landed: %v", want, err)
		}
		if got := chunk.Timestamp; got != want {
			t.Fatalf("got ts %d, want %d", got, want)
		}
	}

	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("caught up: got %v, want io.EOF", err)
	}
}

// A length prefix split across two flushes is the other half of the same
// problem, and the narrower one - only four bytes wide.
func TestTailReaderResumesAcrossAPartialLengthPrefix(t *testing.T) {
	data, ends := encoded(t, delta(1000, 1), delta(2000, 2))

	path := filepath.Join(t.TempDir(), "live.mltn")
	if err := os.WriteFile(path, data[:ends[0]+2], 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tr := NewTailReader(f)

	if _, err := tr.Next(); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("two bytes of a length prefix: got %v, want io.EOF", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	chunk, err := tr.Next()
	if err != nil {
		t.Fatalf("after the write landed: %v", err)
	}
	if got := chunk.Timestamp; got != 2000 {
		t.Fatalf("got ts %d, want 2000", got)
	}
}

func TestReadHeaderRequiresAHeaderFirst(t *testing.T) {
	data, _ := encoded(t, delta(1000, 1))

	_, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(data)))
	if !errors.Is(err, ErrNotHeader) {
		t.Fatalf("got %v, want ErrNotHeader", err)
	}
}

func TestReadHeaderReturnsTheOpeningWindow(t *testing.T) {
	header := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: &ore.Header{Version: FormatVersion, Name: "s", StartedAt: 1},
	}}
	sized := &ore.RecordingChunk{
		Timestamp: 2,
		Payload: &ore.RecordingChunk_Resize{
			Resize: &ore.Resize{Width: 64, Height: 32, MinX: -8, MinY: -4},
		},
	}

	data, _ := encoded(t, header, sized, delta(3000, 1))

	cr := NewChunkReader(bytes.NewReader(data))
	gotHeader, window, openedAt, err := ReadHeader(cr)
	if err != nil {
		t.Fatal(err)
	}

	if window != (Window{Width: 64, Height: 32, MinX: -8, MinY: -4}) {
		t.Fatalf("got %+v, want 64x32 at -8,-4", window)
	}
	/* The opening chunk's own timestamp, which is where the season's clock
	 * starts - and which lives on the chunk now, not on the Resize. */
	if gotHeader.Name != "s" || openedAt != 2 {
		t.Fatal("header and the moment it opened must come back too")
	}

	// left positioned on the third chunk, so the caller carries straight on
	next, err := cr.Next()
	if err != nil || next.GetDelta() == nil {
		t.Fatalf("expected to be positioned on the delta, got %v / %v", next, err)
	}
}

/*
The window lives on Resize chunks and nowhere else, so a header not followed by
one is a file nothing can decode: a pixel names a coordinate, and without a
size and a corner there is no way to know which coordinates the canvas covers.
*/
func TestReadHeaderRequiresAnOpeningResize(t *testing.T) {
	header := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: &ore.Header{Version: FormatVersion, Name: "s", StartedAt: 1},
	}}
	contents := delta(2, 1)

	data, _ := encoded(t, header, contents)

	_, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(data)))
	if !errors.Is(err, ErrMissingInitialResize) {
		t.Fatalf("got %v, want ErrMissingInitialResize", err)
	}
}

// A window of no area is not a window.
func TestReadHeaderRequiresTheOpeningResizeToHaveArea(t *testing.T) {
	header := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: &ore.Header{Version: FormatVersion, Name: "s", StartedAt: 1},
	}}
	empty := &ore.RecordingChunk{
		Timestamp: 2,
		Payload:   &ore.RecordingChunk_Resize{Resize: &ore.Resize{Width: 8}},
	}

	data, _ := encoded(t, header, empty)

	_, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(data)))
	if !errors.Is(err, ErrMissingInitialResize) {
		t.Fatalf("got %v, want ErrMissingInitialResize", err)
	}
}

/*
A record.v1 file is refused twice over, and the version is what catches it
first now.

It also put the size on the Header, whose width/height are reserved - so those
bytes decode into nothing and there would be no size to read either. Both are
the intended outcome of retiring the fields rather than a regression; the
version check just gets there sooner and says something more useful about why.
*/
func TestReadHeaderRefusesALegacySizeOnHeader(t *testing.T) {
	// fields 2 and 3 on a Header are what record.v1 wrote its size into
	legacyHeader := []byte{0x08, 0x01, 0x10, 0xc8, 0x01, 0x18, 0xc8, 0x01}

	var buf bytes.Buffer
	w := NewChunkWriter(&buf)
	if err := w.Write(&ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: func() *ore.Header {
			h := &ore.Header{}
			// unknown fields survive a round-trip, which is exactly how a v1
			// header looks to this build
			h.ProtoReflect().SetUnknown(legacyHeader[2:])
			h.Version = 1
			return h
		}(),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(&ore.RecordingChunk{
		Timestamp: 2,
		Payload: &ore.RecordingChunk_Resize{
			Resize: &ore.Resize{Width: 8, Height: 8},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(buf.Bytes())))
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got %v, want ErrUnknownVersion - a v1 file is not readable here", err)
	}
}

/*
The whole reason the version stopped being decorative at v6.

A v5 file is structurally identical to a v6 one - same chunks, same field
numbers - and differs only in what the two numbers inside a PixelData mean.
Nothing in the bytes gives that away, so without this check the file reads
cleanly and lays every pixel out wrong. v7 went further and put a Resize on
field 2 of a RecordingChunk, which was free before.
*/
func TestReadHeaderRefusesAStructurallyValidOlderVersion(t *testing.T) {
	header := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: &ore.Header{Version: FormatVersion - 1, Name: "s", StartedAt: 1},
	}}
	sized := &ore.RecordingChunk{
		Timestamp: 2,
		Payload: &ore.RecordingChunk_Resize{
			Resize: &ore.Resize{Width: 64, Height: 32, MinX: -8, MinY: -4},
		},
	}

	data, _ := encoded(t, header, sized)

	_, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(data)))
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got %v, want ErrUnknownVersion", err)
	}
}

// Absent fields are zero, which is the right reading for a season that starts
// on the origin - and the only one available for a corner that never moved.
func TestReadWindowDefaultsToTheOrigin(t *testing.T) {
	if got := ReadWindow(&ore.Resize{Width: 8, Height: 8}); got.MinX != 0 || got.MinY != 0 {
		t.Fatalf("got %+v - an absent corner is the origin", got)
	}
}

// The corner is a coordinate, not a distance, so it goes negative the moment a
// season is expanded leftwards or upwards.
func TestReadWindowReadsNegativeCorners(t *testing.T) {
	got := ReadWindow(&ore.Resize{Width: 8, Height: 8, MinX: -12, MinY: -4})

	if got.MinX != -12 || got.MinY != -4 {
		t.Fatalf("got %+v, want a corner at -12,-4", got)
	}
}
