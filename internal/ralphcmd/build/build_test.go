package build

import (
	"runtime/debug"
	"testing"
)

func TestSentinels(t *testing.T) {
	if VersionDev != "dev" {
		t.Errorf("VersionDev = %q, want %q", VersionDev, "dev")
	}
	if CommitNone != "none" {
		t.Errorf("CommitNone = %q, want %q", CommitNone, "none")
	}
	if DateUnknown != "unknown" {
		t.Errorf("DateUnknown = %q, want %q", DateUnknown, "unknown")
	}
}

func TestDefaultsAreSentinels(t *testing.T) {
	// Package vars must start at the sentinel values so unset paths
	// (go run, plain go build) hit the debug.BuildInfo fallback.
	if Version != VersionDev {
		t.Errorf("Version default = %q, want %q", Version, VersionDev)
	}
	if Commit != CommitNone {
		t.Errorf("Commit default = %q, want %q", Commit, CommitNone)
	}
	if Date != DateUnknown {
		t.Errorf("Date default = %q, want %q", Date, DateUnknown)
	}
}

// fakeBuildInfo returns a closure usable as resolveInfo's readBuild
// arg, sourcing the given settings.
func fakeBuildInfo(ok bool, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		if !ok {
			return nil, false
		}
		return &debug.BuildInfo{Settings: settings}, true
	}
}

func TestResolveInfo_LdflagsWin(t *testing.T) {
	// When ldflags injected a real version, debug.BuildInfo must be ignored
	// completely — the explicit value is authoritative.
	got := resolveInfo("v1.2.3", "abc123", "2026-05-15",
		fakeBuildInfo(true,
			debug.BuildSetting{Key: "vcs.revision", Value: "should-be-ignored"},
			debug.BuildSetting{Key: "vcs.time", Value: "should-be-ignored"},
			debug.BuildSetting{Key: "vcs.modified", Value: "true"},
		))
	want := Triple{Version: "v1.2.3", Commit: "abc123", Date: "2026-05-15"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveInfo_FallsBackToBuildInfo(t *testing.T) {
	got := resolveInfo(VersionDev, CommitNone, DateUnknown,
		fakeBuildInfo(true,
			debug.BuildSetting{Key: "vcs.revision", Value: "deadbeef"},
			debug.BuildSetting{Key: "vcs.time", Value: "2026-05-15T12:00:00Z"},
		))
	want := Triple{Version: VersionDev, Commit: "deadbeef", Date: "2026-05-15T12:00:00Z"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveInfo_DirtySuffix(t *testing.T) {
	got := resolveInfo(VersionDev, CommitNone, DateUnknown,
		fakeBuildInfo(true,
			debug.BuildSetting{Key: "vcs.revision", Value: "deadbeef"},
			debug.BuildSetting{Key: "vcs.modified", Value: "true"},
		))
	if got.Commit != "deadbeef-dirty" {
		t.Errorf("Commit = %q, want %q", got.Commit, "deadbeef-dirty")
	}
}

func TestResolveInfo_DirtySuffixWithoutRevision(t *testing.T) {
	// vcs.modified=true alone — the sentinel still gets suffixed.
	got := resolveInfo(VersionDev, CommitNone, DateUnknown,
		fakeBuildInfo(true,
			debug.BuildSetting{Key: "vcs.modified", Value: "true"},
		))
	if got.Commit != CommitNone+"-dirty" {
		t.Errorf("Commit = %q, want %q", got.Commit, CommitNone+"-dirty")
	}
}

func TestResolveInfo_ModifiedFalseLeavesCommit(t *testing.T) {
	got := resolveInfo(VersionDev, CommitNone, DateUnknown,
		fakeBuildInfo(true,
			debug.BuildSetting{Key: "vcs.revision", Value: "deadbeef"},
			debug.BuildSetting{Key: "vcs.modified", Value: "false"},
		))
	if got.Commit != "deadbeef" {
		t.Errorf("Commit = %q, want %q (no suffix when vcs.modified=false)", got.Commit, "deadbeef")
	}
}

func TestResolveInfo_NoBuildInfo(t *testing.T) {
	got := resolveInfo(VersionDev, CommitNone, DateUnknown,
		fakeBuildInfo(false))
	want := Triple{Version: VersionDev, Commit: CommitNone, Date: DateUnknown}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolveInfo_LdflagsPartialNoFallback(t *testing.T) {
	// If Version is non-sentinel, debug.BuildInfo lookup is skipped entirely
	// even when Commit/Date are still at sentinels — ldflags is
	// authoritative as a set, not per-field.
	got := resolveInfo("v1.0.0", CommitNone, DateUnknown,
		fakeBuildInfo(true,
			debug.BuildSetting{Key: "vcs.revision", Value: "shouldnotappear"},
		))
	if got.Commit != CommitNone || got.Date != DateUnknown {
		t.Errorf("got %+v, want sentinels preserved when Version is set", got)
	}
}

func TestInfoDoesNotMutatePackageVars(t *testing.T) {
	// byob-release.1 invariant: Info() must not mutate Version/Commit/Date.
	v0, c0, d0 := Version, Commit, Date
	_ = Info()
	if Version != v0 || Commit != c0 || Date != d0 {
		t.Errorf("Info() mutated package vars: before=%q/%q/%q after=%q/%q/%q",
			v0, c0, d0, Version, Commit, Date)
	}
}
