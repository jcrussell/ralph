package cmdutil

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrHintExposesUnderlyingError(t *testing.T) {
	base := errors.New("boom")
	h := &ErrHint{Err: base, Hint: "try X"}
	if h.Error() != "boom" {
		t.Errorf("Error() = %q, want %q", h.Error(), "boom")
	}
	if !errors.Is(h, base) {
		t.Errorf("errors.Is(h, base) = false, want true")
	}
}

func TestErrHintIsChainsThroughWrap(t *testing.T) {
	wrapped := fmt.Errorf("%w: extra", ErrNoRepoRoot)
	h := &ErrHint{Err: wrapped, Hint: "init it"}
	if !errors.Is(h, ErrNoRepoRoot) {
		t.Errorf("errors.Is(h, ErrNoRepoRoot) = false, want true (sentinel must chain through ErrHint)")
	}
}

func TestErrHintAsRecoversFlagError(t *testing.T) {
	fe := FlagErrorf("bad flag")
	h := &ErrHint{Err: fe, Hint: "see --help"}
	var got *FlagError
	if !errors.As(h, &got) {
		t.Fatalf("errors.As(h, *FlagError) = false, want true")
	}
	if got.Error() != "bad flag" {
		t.Errorf("got.Error() = %q, want %q", got.Error(), "bad flag")
	}
}

func TestWithHintNilReturnsNil(t *testing.T) {
	if err := WithHint(nil, "anything"); err != nil {
		t.Errorf("WithHint(nil, _) = %v, want nil", err)
	}
}

func TestWithHintAttaches(t *testing.T) {
	base := errors.New("permission denied")
	err := WithHint(base, "chmod the lock dir")
	var h *ErrHint
	if !errors.As(err, &h) {
		t.Fatalf("errors.As(err, *ErrHint) = false")
	}
	if h.Hint != "chmod the lock dir" {
		t.Errorf("Hint = %q, want %q", h.Hint, "chmod the lock dir")
	}
	if !errors.Is(err, base) {
		t.Errorf("errors.Is(err, base) = false, want true")
	}
}
