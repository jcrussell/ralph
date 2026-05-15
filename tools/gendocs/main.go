// Command gendocs generates Markdown reference docs for ralph from
// the cobra command tree. Invoked via `go generate ./...` — the
// directive lives in pkg/cmd/root/root.go.
//
// Usage: go run ./tools/gendocs <output-dir>
//
// Per byob-output.3 + byob-user-docs.2, hand-written cobra Short/Long/
// Example text is the source of truth; this binary projects that text
// into one Markdown file per (sub)command under <output-dir>.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra/doc"

	"github.com/jcrussell/ralph/pkg/cmd/root"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gendocs <output-dir>")
		os.Exit(2)
	}
	out := filepath.Clean(os.Args[1])
	if err := os.MkdirAll(out, 0o750); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	r := root.NewCmdRoot(cmdutil.NewFactory())
	r.DisableAutoGenTag = true
	// cobra registers --version lazily inside Execute(); call it
	// explicitly so the flag shows up in the generated markdown.
	r.InitDefaultVersionFlag()

	if err := doc.GenMarkdownTree(r, out); err != nil {
		fmt.Fprintln(os.Stderr, "GenMarkdownTree:", err)
		os.Exit(1)
	}
}
