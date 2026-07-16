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
