package format

import (
	"encoding/binary"
	"errors"
	"io"

	ore "github.com/pixelate-it/molten/ore"
	"google.golang.org/protobuf/proto"
)

var (
	ErrNotHeader              = errors.New("mltm: first chunk is not a Header")
	ErrMissingInitialKeyframe = errors.New("mltm: Header is not followed by an initial Keyframe with canvas size")
	ErrCorruptChunkLength     = errors.New("mltm: invalid chunk length, file is corrupt")
)

const maxChunkSize = 64 << 20

type ChunkReader struct {
	r io.Reader
}

func NewChunkReader(r io.Reader) *ChunkReader {
	return &ChunkReader{r: r}
}

func (cr *ChunkReader) Next() (*ore.RecordingChunk, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(cr.r, lenBuf[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 || length > maxChunkSize {
		return nil, ErrCorruptChunkLength
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(cr.r, buf); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	var chunk ore.RecordingChunk
	if err := proto.Unmarshal(buf, &chunk); err != nil {
		return nil, err
	}
	return &chunk, nil
}

func ReadHeader(cr *ChunkReader) (*ore.Header, *ore.Keyframe, error) {
	first, err := cr.Next()
	if err != nil {
		return nil, nil, err
	}
	header := first.GetHeader()
	if header == nil {
		return nil, nil, ErrNotHeader
	}

	second, err := cr.Next()
	if err != nil {
		return nil, nil, err
	}
	kf := second.GetKeyframe()
	if kf == nil {
		return nil, nil, ErrMissingInitialKeyframe
	}

	if _, _, ok := InitialSize(header, kf); !ok {
		return nil, nil, ErrMissingInitialKeyframe
	}

	return header, kf, nil
}
