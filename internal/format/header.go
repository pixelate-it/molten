package format

import ore "github.com/pixelate-it/molten/ore"

func InitialSize(header *ore.Header, firstKeyframe *ore.Keyframe) (width, height uint32, ok bool) {
	if header.Width != nil && header.Height != nil {
		return *header.Width, *header.Height, true
	}
	if firstKeyframe.Width != nil && firstKeyframe.Height != nil {
		return *firstKeyframe.Width, *firstKeyframe.Height, true
	}
	return 0, 0, false
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
