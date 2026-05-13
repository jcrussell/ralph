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

	"github.com/jcrussell/ralph/pkg/cmd/root"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/spf13/cobra/doc"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gendocs <output-dir>")
		os.Exit(2)
	}
	out := os.Args[1]
	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir:", err)
		os.Exit(1)
	}

	r := root.NewCmdRoot(cmdutil.NewFactory())
	r.DisableAutoGenTag = true

	if err := doc.GenMarkdownTree(r, out); err != nil {
		fmt.Fprintln(os.Stderr, "GenMarkdownTree:", err)
		os.Exit(1)
	}
}
