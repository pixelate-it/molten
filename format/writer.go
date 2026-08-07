package format

import (
	"encoding/binary"
	"io"

	"google.golang.org/protobuf/proto"
)

// ChunkWriter appends length-prefixed chunks to a recording.
type ChunkWriter struct {
	w io.Writer
}

// NewChunkWriter writes chunks to w.
func NewChunkWriter(w io.Writer) *ChunkWriter {
	return &ChunkWriter{w: w}
}

/*
Write appends one chunk: a big-endian uint32 length, then that many bytes of
encoded message. msg is normally an [ore.RecordingChunk].

The length prefix and the body are two writes, so a process killed between them
leaves a torn tail - which the format allows for and readers stop at. What it
does not allow for is interleaving: one recording, one writer.

Nothing here checks the message against [MaxChunkSize]. A resize keyframe has
to carry the whole canvas, and a canvas large enough to overflow a chunk is a
decision to make where the canvas is sized, not where the bytes go out.
*/
func (cw *ChunkWriter) Write(msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))

	if _, err := cw.w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = cw.w.Write(data)
	return err
}
