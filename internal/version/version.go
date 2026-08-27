// Package version exposes build metadata injected at link time.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Injected via -ldflags "-X github.com/.../internal/version.Version=..."
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info describes this binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Get returns the build metadata, falling back to VCS stamps recorded by the Go
// toolchain when the link-time values were not supplied.
func Get() Info {
	i := Info{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if i.Commit == "unknown" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					i.Commit = s.Value
				case "vcs.time":
					if i.BuildDate == "unknown" {
						i.BuildDate = s.Value
					}
				}
			}
		}
	}
	return i
}

func (i Info) String() string {
	return fmt.Sprintf("mas %s (commit %s, built %s, %s, %s)",
		i.Version, i.Commit, i.BuildDate, i.GoVersion, i.Platform)
}
