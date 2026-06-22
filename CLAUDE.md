# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

rustydocs is a Go CLI that finds **stale documentation** by analyzing git history. It walks a content tree, runs `git blame` per file, groups lines into sections (by Markdown headers) and reports the sections whose most-recent change is older than a threshold — with optional propagation from reusable includes (Hugo shortcodes today). Output is Markdown, HTML, and JSON. Built for docs maintainers and CI.

## Commands

```bash
go build -o rustydocs ./cmd/rustydocs              # build
go test ./...                                       # tests
go vet ./...                                        # vet
gofmt -l .                                          # formatting check (must be empty)
make build                                          # build with version ldflags

# run
./rustydocs --content-dir ./docs --output-dir ./reports --threshold-days 90
./rustydocs --config config.json
go install github.com/nrynss/rustydocs/cmd/rustydocs@latest
```

CI (`.github/workflows/ci.yml`) runs `go build`, `go test`, and `go vet` on Linux, Windows, and macOS, plus a `gofmt` check. The release workflow (`.github/workflows/release.yml`) fires on a `v*` tag and cross-builds linux/darwin/windows × amd64/arm64 with SHA256 checksums. All actions are pinned to commit SHAs.

## Architecture

Pipeline in `cmd/rustydocs/main.go`: flags → `config` → `analyzer.AnalyzeWithProgress` → `report.Generate{Markdown,HTML,JSON}`.

- `internal/config` — JSON config + CLI defaults, `Validate`, staleness tiers, `DetectHugoRoot` (walks up for a `layouts/` dir).
- `internal/analyzer` — `filepath.WalkDir` over the content dir filtered by the `content_extensions` allowlist, a worker pool (`runtime.NumCPU()`, channel-fed) that analyzes each file, and the staleness math (section vs threshold, oldest/most-recent dates, reusable freshness folding).
- `internal/git` — wrappers around `git blame --line-porcelain` (streaming parser) and `git log`; per-directory git-root cache so multiple repos work.
- `internal/parser` — section/paragraph chunking by header regex, reusable-reference detection (regex), and resolution (Hugo shortcode → `layouts/shortcodes/…` + traced data files, or a reusables dir).
- `internal/report` — Markdown (string builder), HTML (`html/template`, embedded via `go:embed`), JSON.

**Zero external dependencies — standard library only. Keep it that way** (goldmark is the one sanctioned exception under consideration, see #27; it's pure-Go). Do not add CGO.

## Gotchas

- **Requires `git` on PATH and full history.** Blame needs an unshallowed clone — CI and any container must use `fetch-depth: 0`.
- **Only files matching `content_extensions` are analyzed** (default `.md`/`.markdown`/`.mdx`; override via config or `--extensions`).
- **The embedded HTML template is `internal/report/templates/report.html`** — the `go:embed` path is relative to the report package.
- **Section detection is a per-line header regex**, so a `#` line inside a fenced code block can be misread as a heading; replacing it with goldmark is tracked in #27.

## Direction (see GitHub issues / epics)

- **Positioning:** CI-first staleness *triage* (age-based, section-level, format-agnostic), **not** code↔doc *drift* correctness. Lean into the GitHub Action (#29) and packaging (#30).
- **Tool-agnostic profiles** (#10): Hugo (done) + Mintlify/Starlight/MkDocs/Docusaurus/RST/AsciiDoc/… Starlight is first-class (#18).
- **Agentic drift** (#26): rustydocs does cheap triage; an AI agent verifies real drift on the shortlist (#25). ast-grep stays optional/pluggable (#28).
