package version

import (
	"context"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestFormat_VersionOnly(t *testing.T) {
	got := format(build.BuildInfo{Version: build.VersionDev, Commit: build.CommitNone, Date: build.DateUnknown})
	if got != "ralph dev" {
		t.Errorf("format = %q, want %q", got, "ralph dev")
	}
}

func TestFormat_WithCommit(t *testing.T) {
	got := format(build.BuildInfo{Version: "v1.0.0", Commit: "abc123", Date: build.DateUnknown})
	if got != "ralph v1.0.0 (abc123)" {
		t.Errorf("format = %q, want %q", got, "ralph v1.0.0 (abc123)")
	}
}

func TestFormat_WithDate(t *testing.T) {
	got := format(build.BuildInfo{Version: "v1.0.0", Commit: build.CommitNone, Date: "2026-05-15T12:00:00Z"})
	if got != "ralph v1.0.0 built 2026-05-15T12:00:00Z" {
		t.Errorf("format = %q, want %q", got, "ralph v1.0.0 built 2026-05-15T12:00:00Z")
	}
}

func TestFormat_FullTriple(t *testing.T) {
	got := format(build.BuildInfo{Version: "v1.2.3", Commit: "deadbeef", Date: "2026-05-15T12:00:00Z"})
	want := "ralph v1.2.3 (deadbeef) built 2026-05-15T12:00:00Z"
	if got != want {
		t.Errorf("format = %q, want %q", got, want)
	}
}

func TestFormat_DirtyCommit(t *testing.T) {
	// vcs.modified=true suffix passes through cleanly.
	got := format(build.BuildInfo{Version: build.VersionDev, Commit: "deadbeef-dirty", Date: build.DateUnknown})
	if got != "ralph dev (deadbeef-dirty)" {
		t.Errorf("format = %q, want %q", got, "ralph dev (deadbeef-dirty)")
	}
}

func TestVersionRun_WritesToOutNotErrOut(t *testing.T) {
	io, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	if err := versionRun(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("versionRun: %v", err)
	}
	if !strings.HasPrefix(bufs.Out.String(), "ralph ") {
		t.Errorf("Out = %q, want prefix 'ralph '", bufs.Out.String())
	}
	if bufs.ErrOut.Len() != 0 {
		t.Errorf("ErrOut = %q, want empty (version data must not go to stderr)", bufs.ErrOut.String())
	}
}

func TestVersionRun_OutputEndsWithNewline(t *testing.T) {
	io, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	if err := versionRun(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("versionRun: %v", err)
	}
	if !strings.HasSuffix(bufs.Out.String(), "\n") {
		t.Errorf("Out = %q, want trailing newline", bufs.Out.String())
	}
}
