/*
Package format reads and writes the .mltn container: a bare sequence of
length-prefixed protobuf chunks, described in full in the repository README.

Nothing here interprets a recording - it hands back [ore.RecordingChunk] values
in the order they were written. See package canvas for replaying those into a
picture or into a checksum.

Two properties of the framing shape everything in this package:

  - It is self-delimiting forwards only. There is no index and no back-pointer,
    so a reader either starts at byte 0 or at an offset it learned earlier.
  - It is append-only and crash-tolerant. A writer killed mid-chunk leaves a
    torn tail, which costs exactly that chunk; readers stop at the first chunk
    that does not parse and treat it as the end.

That second property is also what makes a recording readable while it is still
being written - see [TailReader], which is the only thing here safe to point at
a live file.
*/
package format

import (
	"encoding/binary"
	"errors"
	"io"

	ore "github.com/pixelate-it/molten/ore"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrNotHeader means the file does not begin with a Header chunk, so it is
	// not a recording.
	ErrNotHeader = errors.New("mltm: first chunk is not a Header")
	// ErrMissingInitialKeyframe means nothing established the canvas size: the
	// Header was not followed by a Keyframe carrying width and height. A
	// record.v1 file lands here too, having put the size on the Header, whose
	// width/height are now reserved.
	ErrMissingInitialKeyframe = errors.New("mltm: Header is not followed by an initial Keyframe with canvas size")
	// ErrCorruptChunkLength means a length prefix was zero or above
	// MaxChunkSize. Both readings are treated as corruption rather than as a
	// very large chunk.
	ErrCorruptChunkLength = errors.New("mltm: invalid chunk length, file is corrupt")
)

/*
MaxChunkSize is the largest chunk any reader will accept, and therefore a real
constraint on writers: a resize keyframe is a single chunk and has to carry the
whole canvas.

At roughly 34 bytes per PixelData that runs out near 1.97M pixels, which is why
the backend caps a canvas well below it.
*/
const MaxChunkSize = 64 << 20

/*
ChunkReader reads chunks from a finished recording.

It is deliberately not safe against a file that is still being appended to: a
chunk that is only half-written is consumed and reported as [io.EOF], leaving
the reader positioned inside a chunk body with no way back. Reading a live file
means [TailReader].
*/
type ChunkReader struct {
	r io.Reader
}

// NewChunkReader reads chunks from r. Wrap a file in a bufio.Reader; this does
// no buffering of its own.
func NewChunkReader(r io.Reader) *ChunkReader {
	return &ChunkReader{r: r}
}

/*
Next returns the next chunk, or [io.EOF] at the end of the recording.

A torn final chunk is reported as io.EOF too, which is the format's rule: the
history in front of a torn write stays valid, and the torn chunk is simply not
part of it.
*/
func (cr *ChunkReader) Next() (*ore.RecordingChunk, error) {
	length, err := readLength(cr.r)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(cr.r, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	return unmarshalChunk(buf)
}

/*
TailReader reads a recording that is still being written.

The difference from [ChunkReader] is the only thing that matters for a live
consumer: when the file ends part-way through a chunk, this rewinds to where
that chunk began before reporting [io.EOF]. So the usual loop is correct rather
than merely usual:

	for {
		chunk, err := tr.Next()
		if errors.Is(err, io.EOF) {
			time.Sleep(pollInterval)   // nothing more *yet*
			continue
		}
		if err != nil {
			return err
		}
		...
	}

Without the rewind that loop silently corrupts. ChunkReader.Next consumes the
length prefix and whatever body had been flushed, so the next call reads four
bytes from the middle of a chunk body and takes them for a length - which is
either an ErrCorruptChunkLength out of nowhere or a plausible-looking length
that decodes garbage. A backend flushing a delta every few seconds puts a
polling reader at the end of the file essentially always, so it meets a
half-written chunk almost every time.

There is no signal for "the recording is over": [io.EOF] means only that there
is nothing more right now. A Footer chunk is what says the season is finished.
*/
type TailReader struct {
	rs io.ReadSeeker
}

/*
NewTailReader reads chunks from rs, which must be seekable - an *os.File is.

Do not wrap the file in a bufio.Reader: this needs to seek the thing it reads
from, and a buffered reader hides the real offset. It reads a chunk at a time
instead, which costs two syscalls per chunk and is nothing against a recording
that gains a chunk every few seconds.
*/
func NewTailReader(rs io.ReadSeeker) *TailReader {
	return &TailReader{rs: rs}
}

// Next returns the next complete chunk. [io.EOF] means the recording has no
// further complete chunk *yet*; the offset is left at the start of whatever is
// incomplete, so calling again later resumes cleanly.
func (tr *TailReader) Next() (*ore.RecordingChunk, error) {
	start, err := tr.rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}

	chunk, err := tr.next()
	if errors.Is(err, io.EOF) {
		// Back to the start of the chunk that is not all there, so the bytes
		// already flushed are read again with the rest of them.
		if _, seekErr := tr.rs.Seek(start, io.SeekStart); seekErr != nil {
			return nil, seekErr
		}
		return nil, io.EOF
	}

	return chunk, err
}

// Offset reports where in the file the next chunk will be read from. Worth
// keeping across restarts: it is the only way to resume a recording without
// replaying it from the beginning.
func (tr *TailReader) Offset() (int64, error) {
	return tr.rs.Seek(0, io.SeekCurrent)
}

func (tr *TailReader) next() (*ore.RecordingChunk, error) {
	length, err := readLength(tr.rs)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(tr.rs, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	return unmarshalChunk(buf)
}

func readLength(r io.Reader) (uint32, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, io.EOF
		}
		return 0, err
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 || length > MaxChunkSize {
		return 0, ErrCorruptChunkLength
	}
	return length, nil
}

func unmarshalChunk(buf []byte) (*ore.RecordingChunk, error) {
	var chunk ore.RecordingChunk
	if err := proto.Unmarshal(buf, &chunk); err != nil {
		return nil, err
	}
	return &chunk, nil
}
