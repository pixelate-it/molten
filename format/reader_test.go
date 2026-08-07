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

func delta(ts uint64, ids ...uint32) *ore.RecordingChunk {
	changes := make([]*ore.PixelData, len(ids))
	for i, id := range ids {
		changes[i] = &ore.PixelData{Id: id, Color: 0x112233}
	}
	return &ore.RecordingChunk{Payload: &ore.RecordingChunk_Delta{
		Delta: &ore.Delta{Timestamp: ts, Changes: changes},
	}}
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
		if got := chunk.GetDelta().Timestamp; got != want {
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
	if got := chunk.GetDelta().Timestamp; got != 1000 {
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
		if got := chunk.GetDelta().Timestamp; got != want {
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
	if got := chunk.GetDelta().Timestamp; got != 2000 {
		t.Fatalf("got ts %d, want 2000", got)
	}
}

func TestReadHeaderRequiresAHeaderFirst(t *testing.T) {
	data, _ := encoded(t, delta(1000, 1))

	_, _, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(data)))
	if !errors.Is(err, ErrNotHeader) {
		t.Fatalf("got %v, want ErrNotHeader", err)
	}
}

func TestReadHeaderReturnsTheOpeningSize(t *testing.T) {
	header := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: &ore.Header{Version: 5, Name: "s", StartedAt: 1},
	}}
	w, h := uint32(64), uint32(32)
	sized := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Keyframe{
		Keyframe: &ore.Keyframe{Timestamp: 2, Width: &w, Height: &h},
	}}

	data, _ := encoded(t, header, sized, delta(3000, 1))

	cr := NewChunkReader(bytes.NewReader(data))
	gotHeader, kf, gotW, gotH, err := ReadHeader(cr)
	if err != nil {
		t.Fatal(err)
	}
	if gotW != 64 || gotH != 32 {
		t.Fatalf("got %dx%d, want 64x32", gotW, gotH)
	}
	if gotHeader.Name != "s" || kf.Timestamp != 2 {
		t.Fatal("header and keyframe must come back too")
	}

	// left positioned on the third chunk, so the caller carries straight on
	next, err := cr.Next()
	if err != nil || next.GetDelta() == nil {
		t.Fatalf("expected to be positioned on the delta, got %v / %v", next, err)
	}
}

/*
The size lives on the opening keyframe and nowhere else, so a header followed
by a keyframe that does not carry one is a file nothing can decode. Pixel ids
are y*width+x; without a width there is nowhere to put a pixel.
*/
func TestReadHeaderRequiresASizedKeyframe(t *testing.T) {
	header := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Header{
		Header: &ore.Header{Version: 5, Name: "s", StartedAt: 1},
	}}
	unsized := &ore.RecordingChunk{Payload: &ore.RecordingChunk_Keyframe{
		Keyframe: &ore.Keyframe{Timestamp: 2},
	}}

	data, _ := encoded(t, header, unsized)

	_, _, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(data)))
	if !errors.Is(err, ErrMissingInitialKeyframe) {
		t.Fatalf("got %v, want ErrMissingInitialKeyframe", err)
	}
}

/*
A record.v1 file put the size on the Header, whose width/height are now
reserved - so the bytes that used to carry it decode into nothing and the file
is refused. That is the intended outcome of retiring the fields, not a
regression: there is no longer anywhere to read a size from, and guessing one
would misplace every pixel.
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
	if err := w.Write(&ore.RecordingChunk{Payload: &ore.RecordingChunk_Keyframe{
		Keyframe: &ore.Keyframe{Timestamp: 2},
	}}); err != nil {
		t.Fatal(err)
	}

	_, _, _, _, err := ReadHeader(NewChunkReader(bytes.NewReader(buf.Bytes())))
	if !errors.Is(err, ErrMissingInitialKeyframe) {
		t.Fatalf("got %v, want ErrMissingInitialKeyframe - a v1 size is unreadable now", err)
	}
}

func TestCanvasSizeNeedsBothAxes(t *testing.T) {
	only := uint32(8)

	if _, _, ok := CanvasSize(&ore.Keyframe{Width: &only}); ok {
		t.Error("a width with no height is not a size")
	}
	if _, _, ok := CanvasSize(&ore.Keyframe{Height: &only}); ok {
		t.Error("a height with no width is not a size")
	}
	if _, _, ok := CanvasSize(&ore.Keyframe{}); ok {
		t.Error("an unsized keyframe has no size")
	}
}

func TestKeyframeOriginDefaultsToZero(t *testing.T) {
	x, y := KeyframeOrigin(&ore.Keyframe{})
	if x != 0 || y != 0 {
		t.Fatalf("got %d,%d - an absent origin is zero", x, y)
	}
}
