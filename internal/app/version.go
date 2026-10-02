package app

import (
	"runtime/debug"
	"strings"
)

// Version is injected at release time:
// -ldflags "-X github.com/yourconscience/tackroom/internal/app.Version=1.2.3".
var Version = ""

// versionString prefers the release version, then the module version that
// `go install ...@vX.Y.Z` records, then the VCS revision of a local build.
func versionString() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
			return "dev-" + setting.Value[:7]
		}
	}
	return "dev"
}
