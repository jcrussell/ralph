# Troubleshooting

Failure modes that ralph emits with a remediation hint. Each section
heading is an anchor referenced from an `ErrHint` in the source. Once
shipped, an anchor outlives the error type — renaming a heading
breaks the link from binaries already in the wild.

To add an entry: pick a kebab-case anchor, add the section here, and
reference `https://github.com/jcrussell/ralph/blob/main/docs/troubleshooting.md#<anchor>`
from the `ErrHint.Hint` string in the source. The compat-contract test
in `pkg/cmdutil` ensures the anchor exists.

## no-repo-root

**Symptom.** `ralph` (any subcommand that needs a project) prints:

```
error: no .ralph or .git directory found in any ancestor (searched from <cwd>)
hint: run `ralph init` in your project root, or cd into a git repo
      (see: https://github.com/jcrussell/ralph/blob/main/docs/troubleshooting.md#no-repo-root)
```

**Cause.** Ralph walks up from the current directory looking for a
`.ralph/` or `.git/` directory and never finds one. Either the
project hasn't been initialized yet or the shell is in a directory
outside the project tree.

**Recovery.**

```sh
cd path/to/your/repo
ralph init        # creates .ralph/config.toml and prompts/
```

If the repo is already initialized, just `cd` into it (or any
descendant) before running `ralph`.

**Prevention.** None needed — this is a discovery error, not a misuse
error. Future shells just need to start inside the project.
