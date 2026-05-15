package promptlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	dir := PromptsDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestRenderVariables(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"clean.md": "iter={{.Iter}} state={{.State}} prev={{.PrevState}} dirty={{.GitDirty}} head={{.GitHead}} root={{.RepoRoot}} gate={{.GateResult}}",
	})
	out, err := Render(repo, "clean", Vars{
		Iter: 4, State: "clean", PrevState: "dirty",
		GitDirty: false, GitHead: "abc123", RepoRoot: "/r",
		GateResult: "passed",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "iter=4 state=clean prev=dirty dirty=false head=abc123 root=/r gate=passed"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderReviewVars(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"review.md": "branch={{.Review.Branch}} base={{.Review.Base}} open={{.Review.OpenFindings}}",
	})
	out, err := Render(repo, "review", Vars{Review: ReviewVars{Branch: "feat-x", Base: "main", OpenFindings: 3}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "branch=feat-x base=main open=3" {
		t.Errorf("Render = %q", out)
	}
}

func TestRenderHeaderFooterAutoWrap(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"_header.md": "HDR",
		"_footer.md": "FTR",
		"clean.md":   "BODY {{.State}}",
	})
	out, err := Render(repo, "clean", Vars{State: "clean"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "HDR\nBODY clean\nFTR"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderHeaderFooterOptional(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"clean.md": "BODY",
	})
	out, err := Render(repo, "clean", Vars{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "BODY" {
		t.Errorf("Render = %q, want %q", out, "BODY")
	}
}

func TestRenderMissingStateIsError(t *testing.T) {
	repo := setupRepo(t, nil)
	_, err := Render(repo, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for missing state template")
	}
}

func TestRenderInclude(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"snippets/golden-path.md": "STEP1\nSTEP2",
		"clean.md":                "before\n{{include \"snippets/golden-path.md\"}}\nafter",
	})
	out, err := Render(repo, "clean", Vars{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "before\nSTEP1\nSTEP2\nafter"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderIncludeRejectsEscape(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"clean.md": "{{include \"../../etc/passwd\"}}",
	})
	_, err := Render(repo, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes prompts directory") {
		t.Errorf("err = %v, want escape rejection", err)
	}
}

func TestRenderIncludeRejectsSymlinkEscape(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"clean.md": `{{include "escape"}}`,
	})
	// Target lives outside .ralph/prompts/; the symlink lives inside.
	outside := filepath.Join(repo, "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(PromptsDir(repo), "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	_, err := Render(repo, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for symlink escape")
	}
	if !strings.Contains(err.Error(), "escapes prompts directory") {
		t.Errorf("err = %v, want escape rejection", err)
	}
}

func TestRenderIncludeRejectsAbsolute(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"clean.md": "{{include \"/etc/passwd\"}}",
	})
	_, err := Render(repo, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for absolute include")
	}
}

func TestRenderMissingFieldIsError(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"clean.md": "{{.NoSuchField}}",
	})
	_, err := Render(repo, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}

func TestRenderStringUsesHeaderFooter(t *testing.T) {
	repo := setupRepo(t, map[string]string{
		"_header.md": "HDR",
		"_footer.md": "FTR",
	})
	out, err := RenderString(repo, "BODY {{.State}}", Vars{State: "dirty"})
	if err != nil {
		t.Fatalf("RenderString: %v", err)
	}
	if out != "HDR\nBODY dirty\nFTR" {
		t.Errorf("RenderString = %q", out)
	}
}
