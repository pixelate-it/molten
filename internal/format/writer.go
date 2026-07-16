package format

import (
	"encoding/binary"
	"io"

	"google.golang.org/protobuf/proto"
)

type ChunkWriter struct {
	w io.Writer
}

func NewChunkWriter(w io.Writer) *ChunkWriter {
	return &ChunkWriter{w: w}
}

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
