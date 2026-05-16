# Credits and Lineage

The architectural shape of this project — its decisions, idioms, and
the test/lint surface — traces back to a handful of upstream sources.
Code under this repository is original; credit for the underlying ideas
belongs to the authors below. Mistakes and over-generalizations in the
distillation belong to this repository.

## github.com/jcrussell/byob-go-cli

Ralph is bootstrapped from the byob-go-cli template. Every entry in
`bd list --type=decision` is a `byob-*` decision seeded from that
template; ralph either follows it as written or records a deliberate
deviation (see e.g. the `storage-rules` memory or
`config-provenance-deviation`).

Upstream: <https://github.com/jcrussell/byob-go-cli>

## github.com/cli/cli (the `gh` CLI)

Most of the architectural patterns in ralph originate from the `gh`
CLI codebase. The `gh` team did the hard thinking for:

- Central `Factory` struct with lazy-closure dependencies injected into
  every command
- The `Options` + `NewCmdXxx(f, runF)` + pure runFunc three-part command
  shape, including the `runF` test-injection hook inside `RunE`
- Semantic error types (`FlagError`, `SilentError`, `CancelError`) mapped
  to distinct exit codes by a top-level runner
- `IOStreams` abstraction wrapping In/Out/ErrOut with TTY detection and
  a `NO_COLOR`-aware `ColorScheme`
- Cobra command groups for readable `--help` organization
- `ErrHint` wrapper for attaching user-facing remediation text to errors

Upstream: <https://github.com/cli/cli>

## spf13/cobra

Ralph uses `cobra` as its command substrate. Several idioms in the
cobra codebase reward explicit surfacing rather than leaving agents to
discover them the hard way:

- Ship shell completions via cobra's auto-generated `completion <shell>`
  subcommand
- Set `SilenceUsage` and `SilenceErrors` on the root to stop cobra from
  dumping the usage blob on runtime errors
- `PersistentPreRunE` on the root command as app-wide middleware
- Generate reference docs (Markdown, man pages) from the cobra tree via
  the `cobra/doc` package
- `MarkFlagsMutuallyExclusive` / `MarkFlagsRequiredTogether` /
  `MarkFlagsOneRequired` as the declarative way to validate flag
  relationships (integrates with shell completion)

Upstream: <https://github.com/spf13/cobra>

## Go source tree (standard library + `cmd/go`)

A second set of idioms comes directly from the Go source. The Go project
is one of the most idiomatic Go codebases in existence, and several
patterns are stdlib-endorsed or demonstrated by `cmd/go` itself:

- `signal.NotifyContext` for graceful Ctrl-C handling via context
  cancellation
- `context.Context` threaded through every runFunc
- `t.Helper()`, `t.Cleanup()`, `t.TempDir()` for test ergonomics
- `fmt.Errorf("...: %w", err)` as the canonical error-wrap verb
- `fs.FS` + `fstest.MapFS` as a testable filesystem seam
- `flag.Value` / `pflag.Value` for structured custom flag types
- `sync.OnceValue[T]` / `sync.OnceValues[A, B]` for lazy, type-safe
  memoization
- `log/slog` as the single logging surface, with `TextHandler` over
  IOStreams

Upstream: <https://github.com/golang/go>

## Effective Go

<https://go.dev/doc/effective_go> is somewhat dated — some of its advice
(package-level `init()` functions, certain concurrency idioms) has aged
out of modern practice — but three stated conventions still match
current practice and are codified here:

- Accept interfaces, return concrete types
- Compile-time interface assertions with the blank identifier
  (`var _ Iface = (*Concrete)(nil)`)
- Error messages: lowercase, no trailing punctuation, no newlines, so
  they compose cleanly under wrapping

## Go Code Review Comments wiki

<https://go.dev/wiki/CodeReviewComments> is the community-maintained
list of style rules Go reviewers cite. Most of the always-on style
guidance inherited from byob-go-cli — receiver naming, no-`Get`
prefix, doc-comment shape, no blank-error discards, goroutine exit
paths, context as the first parameter, pass-by-value defaults, got/want
ordering, error-message style, initialism casing — distils rules
stated there. The wiki is also the authoritative source for the
"avoid in-band error values" guidance behind `byob-errors.5`.

## Google Go Style

<https://google.github.io/styleguide/go/decisions> is the public Google
Go style guide, structured as a set of "decisions" with rationale (the
same shape as the byob-* decisions tree). It restates and extends the
Code Review Comments wiki; most memories that cite the wiki cite this
guide too.

## Third-party libraries and tools

Ralph depends on a small, deliberate set of external libraries and
tools. Each is named in the decision or memory where the choice (and
its swap-out story) is spelled out.

- `go-git/go-git` — pure-Go git surface. Ralph's `internal/git` is the
  single in-process git boundary (see the `git-package-surface`
  memory). Upstream: <https://github.com/go-git/go-git>
- `BurntSushi/toml` — config decoder for `.ralph/config.toml`.
  Upstream: <https://github.com/BurntSushi/toml>
- `google/go-cmp` — `cmp.Diff` is the standard for non-trivial test
  assertions (see the `cmp-diff-idiom` memory).
  Upstream: <https://github.com/google/go-cmp>
- `goreleaser/goreleaser` — release pipeline (cross-compile matrix,
  archives, checksums, changelog). See the `goreleaser-config` and
  `release-workflow` memories. Upstream:
  <https://github.com/goreleaser/goreleaser>
- `golang.org/x/vuln/cmd/govulncheck` — pinned vulnerability scanner
  wired into CI and the Makefile (see the `govulncheck-wiring`
  memory). Upstream: <https://golang.org/x/vuln>
- `golangci-lint` — lint floor (see the `lint-floor-rationale`
  memory). Upstream: <https://github.com/golangci/golangci-lint>
- `jcrussell/beads` (`bd`) — task tracker, decision log, and memory
  store. Ralph shells out to `bd` for all task state rather than
  reimplementing it (see the `feedback-no-replication` memory).
  Upstream: <https://github.com/jcrussell/beads>
