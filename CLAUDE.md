# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

rustydocs is a Go CLI that finds **stale documentation** by analyzing git history. It walks a content tree, runs `git blame` per file, groups lines into sections (by Markdown headers) and reports the sections whose most-recent change is older than a threshold — with optional propagation from reusable includes (Hugo shortcodes today). Output is Markdown, HTML, and JSON. Built for docs maintainers and CI.

## Commands

```bash
go build -o rustydocs ./cmd/rustydocs              # build
go test ./...                                       # tests (only internal/git has them today)
go vet ./...                                        # vet
gofmt -l .                                          # formatting check (must be empty)
make build                                          # build with version ldflags

# run
./rustydocs --content-dir ./docs --output-dir ./reports --threshold-days 90
./rustydocs --config config.json
go install github.com/nrynss/rustydocs/cmd/rustydocs@latest
```

CI (`.github/workflows/ci.yml`) runs `go build`, `go test`, `go vet`, and a `gofmt` check. The release workflow (`.github/workflows/release.yml`) fires on a `v*` tag and cross-builds linux/darwin/windows × amd64/arm64 with SHA256 checksums.

## Architecture

Pipeline in `cmd/rustydocs/main.go`: flags → `config` → `analyzer.AnalyzeWithProgress` → `report.Generate{Markdown,HTML,JSON}`.

- `internal/config` — JSON config + CLI defaults, `Validate`, staleness tiers, `DetectHugoRoot` (walks up for a `layouts/` dir).
- `internal/analyzer` — `filepath.WalkDir` over the content dir, a worker pool (`runtime.NumCPU()`, channel-fed) that analyzes each file, and the staleness math (section vs threshold, oldest/most-recent dates, reusable freshness folding).
- `internal/git` — wrappers around `git blame --line-porcelain` (streaming parser) and `git log`; per-directory git-root cache so multiple repos work.
- `internal/parser` — section/paragraph chunking by header regex, reusable-reference detection (regex), and resolution (Hugo shortcode → `layouts/shortcodes/…` + traced data files, or a reusables dir).
- `internal/report` — Markdown (string builder), HTML (`html/template`, embedded via `go:embed`), JSON.

**Zero external dependencies — standard library only. Keep it that way** (goldmark is the one sanctioned exception under consideration, see #27; it's pure-Go). Do not add CGO.

## Gotchas

- **The embedded HTML template is `internal/report/templates/report.html`** (the `go:embed` path is relative to the report package). The root `templates/report.html` is a stale, divergent duplicate — don't edit it (issue #6).
- **`internal/git` tests use synthetic blame input.** Real `git blame --line-porcelain` headers are `<40-hex-sha> <orig> <final> <group>` — the tests omit the line numbers, which masks a parsing bug (#21). Test against realistic porcelain output.
- **The walker currently analyzes every non-binary file, not just docs** (#1) — there's no extension allowlist yet, only a binary blocklist.
- **`RelativePath` keeps OS separators**, so reports emit backslashes on Windows and HTML anchors break (#22). Normalize with `filepath.ToSlash` at the source.
- **Requires `git` on PATH and full history.** Blame needs an unshallowed clone — CI must use `fetch-depth: 0`.

## Direction (see GitHub issues / epics)

- **Positioning:** CI-first staleness *triage* (age-based, section-level, format-agnostic), **not** code↔doc *drift* correctness. Lean into the GitHub Action (#29) and packaging (#30).
- **Tool-agnostic profiles** (#10): Hugo (done) + Mintlify/Starlight/MkDocs/Docusaurus/RST/AsciiDoc/… Starlight is first-class (#18).
- **Agentic drift** (#26): rustydocs does cheap triage; an AI agent verifies real drift on the shortlist (#25). ast-grep stays optional/pluggable (#28).
