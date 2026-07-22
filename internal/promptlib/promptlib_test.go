package promptlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, body := range files {
		m[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return m
}

func TestRenderVariables(t *testing.T) {
	fsys := mapFS(map[string]string{
		"clean.md": "iter={{.Iter}} state={{.State}} prev={{.PrevState}} dirty={{.GitDirty}} head={{.GitHead}} root={{.RepoRoot}} gate={{.GateResult}} gatefails={{.GateFailStreak}}",
	})
	out, err := Render(fsys, "clean", Vars{
		Iter: 4, State: "clean", PrevState: "dirty",
		GitDirty: false, GitHead: "abc123", RepoRoot: "/r",
		GateResult: "passed", GateFailStreak: 2,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "iter=4 state=clean prev=dirty dirty=false head=abc123 root=/r gate=passed gatefails=2"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderReviewVars(t *testing.T) {
	fsys := mapFS(map[string]string{
		"review.md": "branch={{.Review.Branch}} base={{.Review.Base}} open={{.Review.OpenFindings}}",
	})
	out, err := Render(fsys, "review", Vars{Review: ReviewVars{Branch: "feat-x", Base: "main", OpenFindings: 3}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "branch=feat-x base=main open=3" {
		t.Errorf("Render = %q", out)
	}
}

func TestBeadsVarsFromExcludeTypes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"empty slice", []string{}, ""},
		{"one", []string{"byob"}, " --exclude-type=byob"},
		{"two", []string{"byob", "convoy"}, " --exclude-type=byob --exclude-type=convoy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BeadsVarsFromExcludeTypes(c.in).ExcludeFlags
			if got != c.want {
				t.Errorf("ExcludeFlags = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderBeadsExcludeFlagsInline(t *testing.T) {
	fsys := mapFS(map[string]string{
		"clean.md": "run: bd ready{{.Beads.ExcludeFlags}}",
	})
	// Default zero value of Vars.Beads is empty — concatenation is safe.
	got, err := Render(fsys, "clean", Vars{})
	if err != nil {
		t.Fatalf("Render zero: %v", err)
	}
	if got != "run: bd ready" {
		t.Errorf("zero render = %q, want %q", got, "run: bd ready")
	}

	got, err = Render(fsys, "clean", Vars{Beads: BeadsVarsFromExcludeTypes([]string{"byob"})})
	if err != nil {
		t.Fatalf("Render filtered: %v", err)
	}
	if got != "run: bd ready --exclude-type=byob" {
		t.Errorf("filtered render = %q, want %q", got, "run: bd ready --exclude-type=byob")
	}
}

func TestRenderHeaderFooterAutoWrap(t *testing.T) {
	fsys := mapFS(map[string]string{
		"_header.md": "HDR",
		"_footer.md": "FTR",
		"clean.md":   "BODY {{.State}}",
	})
	out, err := Render(fsys, "clean", Vars{State: "clean"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "HDR\nBODY clean\nFTR"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderHeaderFooterOptional(t *testing.T) {
	fsys := mapFS(map[string]string{
		"clean.md": "BODY",
	})
	out, err := Render(fsys, "clean", Vars{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "BODY" {
		t.Errorf("Render = %q, want %q", out, "BODY")
	}
}

func TestRenderMissingStateIsError(t *testing.T) {
	_, err := Render(mapFS(nil), "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for missing state template")
	}
}

func TestRenderInclude(t *testing.T) {
	fsys := mapFS(map[string]string{
		"snippets/golden-path.md": "STEP1\nSTEP2",
		"clean.md":                "before\n{{include \"snippets/golden-path.md\"}}\nafter",
	})
	out, err := Render(fsys, "clean", Vars{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "before\nSTEP1\nSTEP2\nafter"
	if out != want {
		t.Errorf("Render = %q, want %q", out, want)
	}
}

func TestRenderIncludeRejectsEscape(t *testing.T) {
	fsys := mapFS(map[string]string{
		"clean.md": "{{include \"../../etc/passwd\"}}",
	})
	_, err := Render(fsys, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes prompts directory") {
		t.Errorf("err = %v, want escape rejection", err)
	}
}

// Symlink-escape protection is delivered by the FS implementation
// passed to Render. Open(repoRoot) returns an *os.Root whose FS()
// uses openat to block symlinks pointing outside the prompts/
// directory. Tests using fstest.MapFS have no symlinks; this test
// exercises the production wiring.
func TestOpenBlocksSymlinkEscape(t *testing.T) {
	repo := t.TempDir()
	dir := PromptsDir(repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.md"), []byte(`{{include "escape"}}`), 0o644); err != nil {
		t.Fatalf("write clean: %v", err)
	}
	outside := filepath.Join(repo, "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(dir, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	root, err := Open(repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = root.Close() }()
	out, err := Render(root.FS(), "clean", Vars{})
	if err == nil {
		t.Fatalf("expected error, got %q", out)
	}
	if strings.Contains(out, "SECRET") {
		t.Errorf("leaked outside content: %q", out)
	}
}

func TestRenderIncludeRejectsAbsolute(t *testing.T) {
	fsys := mapFS(map[string]string{
		"clean.md": "{{include \"/etc/passwd\"}}",
	})
	_, err := Render(fsys, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for absolute include")
	}
}

func TestRenderMissingFieldIsError(t *testing.T) {
	fsys := mapFS(map[string]string{
		"clean.md": "{{.NoSuchField}}",
	})
	_, err := Render(fsys, "clean", Vars{})
	if err == nil {
		t.Fatal("expected error for missing field")
	}
}

func TestRenderStringUsesHeaderFooter(t *testing.T) {
	fsys := mapFS(map[string]string{
		"_header.md": "HDR",
		"_footer.md": "FTR",
	})
	out, err := RenderString(fsys, "BODY {{.State}}", Vars{State: "dirty"})
	if err != nil {
		t.Fatalf("RenderString: %v", err)
	}
	if out != "HDR\nBODY dirty\nFTR" {
		t.Errorf("RenderString = %q", out)
	}
}

func TestFingerprintCoversEveryRegularFile(t *testing.T) {
	fsys := fstest.MapFS{
		"clean.md":           {Data: []byte("body")},
		"_header.md":         {Data: []byte("hdr")},
		"snippets/rules.txt": {Data: []byte("rules")},
	}
	fp, err := Fingerprint(fsys)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	// Includes accept any relpath under the root, so a non-.md snippet is as
	// much a prompt as clean.md is.
	for _, want := range []string{"clean.md", "_header.md", "snippets/rules.txt"} {
		if fp[want] == "" {
			t.Errorf("missing %s from fingerprint: %v", want, fp)
		}
	}
}

// Same bytes must hash the same, or every iteration would report a change.
func TestFingerprintIsStable(t *testing.T) {
	fsys := fstest.MapFS{"clean.md": {Data: []byte("body")}}
	a, err := Fingerprint(fsys)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	b, err := Fingerprint(fsys)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if a["clean.md"] != b["clean.md"] {
		t.Errorf("fingerprint not stable across calls: %q vs %q", a["clean.md"], b["clean.md"])
	}
}

func TestFingerprintMissingDirIsEmptyNotError(t *testing.T) {
	fp, err := Fingerprint(fstest.MapFS{})
	if err != nil {
		t.Fatalf("Fingerprint on an empty FS = %v, want nil", err)
	}
	if len(fp) != 0 {
		t.Errorf("fingerprint = %v, want empty", fp)
	}
}

func TestChangedFiles(t *testing.T) {
	base := map[string]string{"clean.md": "a", "_header.md": "b"}

	tests := []struct {
		name      string
		prev, cur map[string]string
		want      []string
	}{
		{"first sample reports nothing", nil, base, nil},
		{"unchanged", base, map[string]string{"clean.md": "a", "_header.md": "b"}, nil},
		{"modified", base, map[string]string{"clean.md": "z", "_header.md": "b"}, []string{"clean.md"}},
		{"added", base, map[string]string{"clean.md": "a", "_header.md": "b", "dirty.md": "c"}, []string{"dirty.md"}},
		{"removed", base, map[string]string{"clean.md": "a"}, []string{"_header.md"}},
		{"sorted", base, map[string]string{"clean.md": "z", "_header.md": "z"}, []string{"_header.md", "clean.md"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ChangedFiles(tc.prev, tc.cur)
			if len(got) != len(tc.want) {
				t.Fatalf("ChangedFiles = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ChangedFiles = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
