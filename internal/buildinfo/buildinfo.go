// Package buildinfo contains the non-secret metadata stamped into release
// binaries. Development builds intentionally keep stable, explicit fallback
// values so health tooling never has to infer a version from an image tag.
package buildinfo

// These variables are overridden by the release Docker build with -ldflags.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// Info is the safe subset of build metadata exposed by the version endpoint.
// It deliberately contains no configuration, credentials, or dependency
// values.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

func Current() Info {
	return Info{Version: Version, Commit: Commit, BuildTime: BuildTime}
}
