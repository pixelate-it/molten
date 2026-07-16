package format

import (
	"io"

	ore "github.com/pixelate-it/molten/ore"
)

func MaxCanvasSize(reader *ChunkReader, header *ore.Header, initialKf *ore.Keyframe) (uint32, uint32, error) {
	maxW, maxH, _ := InitialSize(header, initialKf)

	for {
		chunk, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				return maxW, maxH, nil
			}
			return 0, 0, err
		}

		if kf := chunk.GetKeyframe(); kf != nil && kf.Width != nil && kf.Height != nil {
			if *kf.Width > maxW {
				maxW = *kf.Width
			}
			if *kf.Height > maxH {
				maxH = *kf.Height
			}
		}
	}
}
