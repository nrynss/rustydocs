# Contributing to rustydocs

Thanks for your interest in improving rustydocs! Bug reports, feature ideas, and
pull requests are all welcome. This is a small project, so the process is light.

By participating you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

rustydocs is a standard Go (1.21+) CLI with **no external dependencies** — the
standard library only. You need a Go toolchain and `git`.

```bash
git clone https://github.com/nrynss/rustydocs.git
cd rustydocs
go build -o rustydocs ./cmd/rustydocs
./rustydocs --content-dir ./docs --threshold-days 90
```

## Development loop

Before opening a pull request, run the same checks CI enforces:

```bash
gofmt -l .        # formatting — must print nothing (run `gofmt -w .` to fix)
go vet ./...      # vet
go test ./...     # tests
```

## Conventions worth knowing

- **Zero external dependencies is a hard rule.** The core is standard-library
  only and cross-compiles to a single static binary with a trivial
  `GOOS`/`GOARCH` matrix. Don't add a module dependency (and never CGO) without
  discussing it first. The one sanctioned exception under review is goldmark
  (pure Go) for Markdown parsing — see #27.
- **rustydocs shells out to `git`.** It needs `git` on `PATH` and the repo's
  **full history** (no shallow clone). Anything reading blame/log lives in
  `internal/git`.
- **The embedded HTML template is `internal/report/templates/report.html`.** The
  root `templates/report.html` is a stale duplicate; don't edit it (#6).
- **Be careful with paths on Windows.** Use `filepath` helpers and normalize
  display/anchor paths with `filepath.ToSlash` (#22).
- **Keep the CLI/output honest** — don't add a flag or report field that only
  half-works.

## Pull requests

- Keep changes focused; one logical change per PR is easiest to review.
- Add or update tests for behavior changes (`go test ./...`).
- Update the `## [Unreleased]` section of [`CHANGELOG.md`](CHANGELOG.md) for any
  user-facing change (the project follows
  [Keep a Changelog](https://keepachangelog.com/) and
  [Semantic Versioning](https://semver.org/)).
- Reference the issue you're addressing (e.g. "Closes #12").
- All CI checks (build, test, vet, gofmt) must pass.

## Project structure

```
cmd/rustydocs/          CLI entry point (main.go)
internal/
  analyzer/             Orchestration — WalkDir, worker pool, staleness math
  config/               Config loading/validation, staleness tiers
  git/                  git blame / git log wrappers, root cache
  parser/               Section/paragraph chunking, reusable detection
  report/               Markdown / HTML (embedded template) / JSON output
```

## Authoring profiles

Built-ins live in `internal/config/profiles/<name>.json` and are compiled into
one binary with `go:embed`. They are developer assets, not external runtime
configuration. Use an existing definition as a starting point and add its name
to `profiles/registry.json`. That explicit order breaks same-directory marker
ties; the nearest marker still wins, and public profile listings are sorted
alphabetically. Keep existing precedence unless a behavior change is intended.

The private manifest schema is defined in `profile_loader.go`; it does not
change user-config JSON. A definition supplies `name`, `description`,
`content_extensions`, `resolver`, and optional `root_markers`,
`marker_predicates`, `reusable_patterns`, `reusable_extensions`, `import_map`,
`include_example`, and `parser_capabilities`. Names must match the registry and
filename. Resolver values are `none`, `hugo`, or `path`. Patterns must compile
with exactly one capture group. Lists reject invalid or duplicate values;
extensions are dot-prefixed. Unknown/duplicate JSON fields, trailing data,
unlisted assets, and invalid bindings fail at package initialization with the
asset named in the error.

Capability flags default to false. `path_base_mode` defaults to `""`
(shared-first); the other supported value is `"page-only"`. Root-absolute
captures use the root in either mode. Omitted or `null`
`allowed_explicit_path_extensions` means unrestricted; `[]` rejects every
explicit extension. Matching is exact and case-sensitive. Extensionless
captures still try effective reusable extensions, including user overrides.
Omitted or `null` `index_file_names` defaults to `["index"]`; `[]` disables
index lookup. Names are extensionless stems without path separators. Lookup
tries the literal path, then extension-suffixed paths, then index candidates
ordered by extension first and index name second: with `[".md", ".mdx"]` and
`["README", "index"]`, indices are `README.md`, `index.md`, `README.mdx`,
`index.mdx`. Registry lookup and parser construction clone the capability
slices while preserving nil versus empty.

`marker_predicates` maps listed file markers to named Go bindings in
`profile_loader.go` (for example, `mintlify-config`). Reuse an existing binding
when appropriate; a new detection algorithm requires a Go predicate and binding.
Directory markers end in `/` and cannot have content predicates. Algorithms
remain in `profile.go`; JSON does not define executable predicates.

`Resolver` and `ImportMap` remain the sole strategy authorities; the analyzer
passes them with effective user patterns/extensions and narrow capabilities.
The parser receives no profile name or whole `Profile`. Compose existing
capabilities for data-only additions; new include/resolution semantics need
generic parser changes and tests, without tool-name branches. Fence masking and
capture skipping are independent. Alias skipping applies only to unresolved
include-pattern captures, not component captures.

Pattern provenance also matters: exact equality with `MDXComponentPattern`
marks component usage, and `patternExtensionConstraints` constrains syntax to
page extensions by regex string. Identical user patterns inherit these rules.
These remain Go contracts; do not assume a new JSON regex automatically carries
component or extension-constrained semantics.

Test profile detection/precedence, defaults and overrides, capability
independence, resolution/provenance, and manifest validation. For refactors,
preserve existing expectations and compare all report formats with fixed test
clocks or paired CLI runs, normalizing only generated metadata. Run build,
tests, race detector, vet, formatting, and coverage (at least 80% per production
package; `internal/testutil` is a test-only helper).

## Reporting bugs / requesting features

Open an issue using the templates. For **security** issues, follow
[`SECURITY.md`](SECURITY.md) — please don't file a public issue. For general
questions, use [Discussions](https://github.com/nrynss/rustydocs/discussions).

When opening issues, maintainers add labels such as `bug`, `enhancement`,
`good first issue`, `git-integration`, `parser`, `report`, `config`,
`performance`, and `windows`.

By contributing, you agree that your contributions are licensed under the
project's [Apache-2.0 license](LICENSE).
