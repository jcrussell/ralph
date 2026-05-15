package ralphcmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/pkg/cmdutil"
)

func TestMapErrNil(t *testing.T) {
	var buf bytes.Buffer
	if code := mapErr(nil, &buf); code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if buf.Len() != 0 {
		t.Errorf("errOut = %q, want empty", buf.String())
	}
}

func TestMapErrCancel(t *testing.T) {
	var buf bytes.Buffer
	if code := mapErr(cmdutil.ErrCancel, &buf); code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
}

func TestMapErrSilent(t *testing.T) {
	var buf bytes.Buffer
	if code := mapErr(cmdutil.ErrSilent, &buf); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if buf.Len() != 0 {
		t.Errorf("ErrSilent must not print, got %q", buf.String())
	}
}

func TestMapErrFlag(t *testing.T) {
	var buf bytes.Buffer
	code := mapErr(cmdutil.FlagErrorf("bad --port %d", 99), &buf)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(buf.String(), "bad --port 99") {
		t.Errorf("errOut = %q, want substring 'bad --port 99'", buf.String())
	}
}

func TestMapErrUnknownCommand(t *testing.T) {
	var buf bytes.Buffer
	err := errors.New(`unknown command "foo" for "ralph"`)
	code := mapErr(err, &buf)
	if code != 2 {
		t.Errorf("code = %d, want 2 (unknown command classifies as FlagError)", code)
	}
	if !strings.Contains(buf.String(), `unknown command "foo"`) {
		t.Errorf("errOut = %q, want unknown-command text", buf.String())
	}
}

func TestMapErrExitCode(t *testing.T) {
	var buf bytes.Buffer
	code := mapErr(&cmdutil.ExitCodeError{Code: 42}, &buf)
	if code != 42 {
		t.Errorf("code = %d, want 42", code)
	}
}

func TestMapErrGeneric(t *testing.T) {
	var buf bytes.Buffer
	code := mapErr(fmt.Errorf("boom"), &buf)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(buf.String(), "error: boom") {
		t.Errorf("errOut = %q, want 'error: boom'", buf.String())
	}
}

func TestClassifyUnknownCommandWrapsUnknownCommand(t *testing.T) {
	got := classifyUnknownCommand(errors.New(`unknown command "foo" for "ralph"`))
	var fe *cmdutil.FlagError
	if !errors.As(got, &fe) {
		t.Errorf("err = %v; want errors.As(_, **FlagError)", got)
	}
}

func TestClassifyUnknownCommandLeavesUnrelated(t *testing.T) {
	got := classifyUnknownCommand(errors.New("connection refused"))
	var fe *cmdutil.FlagError
	if errors.As(got, &fe) {
		t.Errorf("non-unknown-command error must not be wrapped, got %v", got)
	}
}
