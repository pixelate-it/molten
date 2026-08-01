// https://github.com/XTLS/Xray-core/blob/main/core/core.go

package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	VersionYear  byte = 26
	VersionMonth byte = 8
	VersionDay   byte = 1
)

var (
	build    = "dev"
	codename = "Flawless Crystal"
	intro    = "High-performance, protobuf-based stream format for recording and broadcasting real-time pixel grids and collaborative canvases"
)

func init() {
	if build != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	var isDirty bool
	var foundBuild bool

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if len(setting.Value) < 7 {
				return
			}
			build = setting.Value[:7]
			foundBuild = true
		case "vcs.modified":
			isDirty = setting.Value == "true"
		}
	}

	if isDirty && foundBuild {
		build += "-dirty"
	}
}

func Version() string {
	return fmt.Sprintf("%d.%d.%d", VersionYear, VersionMonth, VersionDay)
}

func Statement() []string {
	return []string{
		fmt.Sprintf("Molten %s (%s) %s (%s %s/%s)",
			Version(), codename, build, runtime.Version(), runtime.GOOS, runtime.GOARCH),
		intro,
	}
}
