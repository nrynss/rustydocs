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

## Reporting bugs / requesting features

Open an issue using the templates. For **security** issues, follow
[`SECURITY.md`](SECURITY.md) — please don't file a public issue. For general
questions, use [Discussions](https://github.com/nrynss/rustydocs/discussions).

When opening issues, maintainers add labels such as `bug`, `enhancement`,
`good first issue`, `git-integration`, `parser`, `report`, `config`,
`performance`, and `windows`.

By contributing, you agree that your contributions are licensed under the
project's [Apache-2.0 license](LICENSE).
