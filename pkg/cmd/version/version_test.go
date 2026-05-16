package version

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestFormatShort(t *testing.T) {
	got := FormatShort(build.Triple{Version: "v1.2.3", Commit: "abc123", Date: "2026-05-15"})
	if got != "ralph v1.2.3" {
		t.Errorf("FormatShort = %q, want %q", got, "ralph v1.2.3")
	}
}

func TestFormatShort_DevSentinel(t *testing.T) {
	got := FormatShort(build.Triple{Version: build.VersionDev, Commit: build.CommitNone, Date: build.DateUnknown})
	if got != "ralph dev" {
		t.Errorf("FormatShort = %q, want %q", got, "ralph dev")
	}
}

func TestFormatLong_FullTriple(t *testing.T) {
	got := FormatLong(build.Triple{Version: "v1.2.3", Commit: "deadbeef", Date: "2026-05-15T12:00:00Z"})
	for _, want := range []string{
		"ralph v1.2.3\n",
		"  commit: deadbeef\n",
		"  built:  2026-05-15T12:00:00Z\n",
		"  go:     " + runtime.Version() + "\n",
		"  os:     " + runtime.GOOS + "/" + runtime.GOARCH + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatLong missing %q; full output:\n%s", want, got)
		}
	}
}

func TestFormatLong_SentinelsRenderVerbatim(t *testing.T) {
	// A `go run` with no ldflags resolves to the sentinels. They must
	// appear verbatim — surfacing "unknown" tells the user the build
	// dropped metadata.
	got := FormatLong(build.Triple{Version: build.VersionDev, Commit: build.CommitNone, Date: build.DateUnknown})
	if !strings.Contains(got, "  commit: none\n") {
		t.Errorf("FormatLong should preserve commit sentinel; got %q", got)
	}
	if !strings.Contains(got, "  built:  unknown\n") {
		t.Errorf("FormatLong should preserve date sentinel; got %q", got)
	}
}

func TestFormatLong_StartsWithRalphLine(t *testing.T) {
	got := FormatLong(build.Triple{Version: "v0.0.0", Commit: build.CommitNone, Date: build.DateUnknown})
	if !strings.HasPrefix(got, "ralph v0.0.0\n") {
		t.Errorf("FormatLong should start with 'ralph <version>\\n'; got %q", got)
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

func TestVersionRun_IncludesGoAndOSLines(t *testing.T) {
	// `version` is the richer block — go-version and os/arch are the
	// runtime-resolved fields that distinguish it from `--version`.
	io, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: io}
	if err := versionRun(context.Background(), &Options{F: f}); err != nil {
		t.Fatalf("versionRun: %v", err)
	}
	out := bufs.Out.String()
	if !strings.Contains(out, "  go:     ") {
		t.Errorf("Out missing go-version line: %q", out)
	}
	if !strings.Contains(out, "  os:     "+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("Out missing os/arch line: %q", out)
	}
}
