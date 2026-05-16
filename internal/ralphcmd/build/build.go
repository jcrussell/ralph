// Package build is the single source of truth for ralph's build
// metadata. Both the Makefile and goreleaser inject the same three
// vars via -ldflags -X so callers see identical values regardless of
// build path. When no ldflags are set, Info() falls back to
// debug.ReadBuildInfo() so `go install ...@latest` still produces a
// usable `ralph version`.
package build

import (
	"runtime/debug"
	"sync"
)

// Sentinel defaults — exported so consumers test against a symbol,
// not a string literal.
const (
	VersionDev  = "dev"
	CommitNone  = "none"
	DateUnknown = "unknown"
)

// Build-time injected via -ldflags -X. See Makefile.
var (
	Version = VersionDev
	Commit  = CommitNone
	Date    = DateUnknown
)

// Triple is the resolved version/commit/date triple returned by Info().
type Triple struct {
	Version, Commit, Date string
}

// Info returns the resolved version/commit/date. Authoritative source
// is ldflags; on a default build the function consults
// debug.ReadBuildInfo() for VCS revision and dirty flag so a plain
// `go install` still yields a meaningful answer. Wrapped in
// sync.OnceValue so the scan runs at most once per process
// and concurrent callers do not race.
var Info = sync.OnceValue(func() Triple {
	return resolveInfo(Version, Commit, Date, debug.ReadBuildInfo)
})

// resolveInfo is the testable seam — pure function over its inputs,
// no package-var reads, no mutation. The decision (byob-release.1)
// requires Info() not to mutate the package vars from its read path.
func resolveInfo(v, c, d string, readBuild func() (*debug.BuildInfo, bool)) Triple {
	if v == VersionDev {
		if info, ok := readBuild(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if c == CommitNone {
						c = s.Value
					}
				case "vcs.time":
					if d == DateUnknown {
						d = s.Value
					}
				case "vcs.modified":
					if s.Value == "true" {
						c += "-dirty"
					}
				}
			}
		}
	}
	return Triple{Version: v, Commit: c, Date: d}
}
