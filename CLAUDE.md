# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

rustydocs is a Go CLI that finds **stale documentation** by analyzing git history. It walks a content tree, runs `git blame` per file, groups lines into sections (by Markdown headers) and reports the sections whose most-recent change is older than a threshold — with optional propagation from reusable includes (Hugo shortcodes today). Output is Markdown, HTML, and JSON. Built for docs maintainers and CI.

## Commands

```bash
go build -o rustydocs ./cmd/rustydocs              # build
go test ./...                                       # tests
go test -cover ./...                                # tests with coverage (≥80% per pkg)
go test -race ./...                                 # race detector
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

- `internal/config` — JSON config + CLI defaults, `Validate`, staleness tiers, and the **profile registry** (`profile.go`): a `Profile` bundles content extensions, root markers, reusable patterns and a `Resolver` (forward scaffolding — read nowhere yet; resolution is still driven by whether a Hugo root / reusables dir is set, and the first consumer will be the Mintlify direct-path resolver, #7); built-ins are `markdown` (default, no reusable detection) and `hugo` (markers: a `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config (`hugo.*`/`config.*` under it) — the config-file and `themes/` markers matter because git does not track an empty `layouts/`; a marker may be a relative path, and the trailing-`/` directory rule applies to its last segment). `ApplyProfile` resolves `--profile`/`"profile"` or auto-detects with a nearest-marker walk up from the content dir (`detectNearest`: the first level holding any profile's marker wins, registry order only breaks ties at the same level; `DetectRoot(contentDir, markers)` is the single-profile form of the same walk; `walkUp` stops at the nearest enclosing git repo root — the level holding `.git` is searched, nothing above it — so a checkout under an unrelated `themes/`/`layouts/` dir is not misdetected; the bound is a `.git` directory or a `.git` file pointing at a linked worktree, while a `.git` file pointing into a parent's `.git/modules/` marks a **submodule** and is walked *through* (`gitFileIsSubmodule`), so a Hugo site whose `content/` is a submodule still resolves against the parent's `layouts/`/`hugo.toml`; an unreadable or unparsable `.git` file stops the walk, and only a tree with no `.git` above it walks to the filesystem root) and fills only the settings the user left empty (the legacy reusables-dir flow additionally restores the hugo *extensions*, not just the patterns, so `.mdx` keeps being analyzed) — call it after merging file + CLI config and before `Normalize`; the analyzer calls it itself if `ResolvedProfile` is unset. `Normalize` reconciles the reporting threshold with the staleness tiers (clamps `warning` ≤ `threshold_days`) so a stale section can't be classed `fresh`.
- `internal/analyzer` — `filepath.WalkDir` over the content dir filtered by the `content_extensions` allowlist, a worker pool (`runtime.NumCPU()`, channel-fed) that analyzes each file, and the staleness math (section vs threshold, oldest/most-recent dates, reusable freshness folding). A file with no git history sets `FileAnalysis.HistoryMissing` (reported as *unknown*, never *fresh*). `nowFunc` is a package var so tests can pin "now".
- `internal/git` — wrappers around `git blame --line-porcelain` (streaming parser) and `git log`; per-directory git-root cache so multiple repos work.
- `internal/parser` — section/paragraph chunking by header regex, reusable-reference detection (regex), and resolution (Hugo shortcode → `layouts/shortcodes/…`, then each `themes/<theme>/layouts/shortcodes/…` in Hugo's lookup order (project wins; symlinked theme directories are followed), + traced data files; or a reusables dir).
- `internal/report` — Markdown (string builder), HTML (`html/template`, embedded via `go:embed`), JSON. Shares a `nowFunc` var for deterministic day-deltas in tests; unknown dates render consistently (`—` / level `unknown`) across all three formats.
- `internal/testutil` — test-only helper: `NewRepo(t)` builds a temp git repo and `Commit(when, msg, files)` lands commits with controlled author/committer dates, so blame/log timestamps (and staleness) are deterministic. Skips when `git` is absent.

**Zero external dependencies — standard library only. Keep it that way** (goldmark is the one sanctioned exception under consideration, see #27; it's pure-Go). Do not add CGO.

## Gotchas

- **Requires `git` on PATH and full history.** Blame needs an unshallowed clone — CI and any container must use `fetch-depth: 0`. Files with no resolvable history (uncommitted, shallow, or not a repo) are reported as *unknown* with a stderr warning and a `files_missing_history` count — not silently treated as fresh.
- **Only files matching `content_extensions` are analyzed**, and the default depends on the resolved profile: `markdown` scans `.md`/`.markdown`; `hugo` adds `.mdx`. Set `content_extensions` in config or pass `--extensions` to override. A run that matches zero files prints a stderr warning naming the profile and extensions in force; a run that matches *some* files but skips files whose extension is content under another built-in profile (`config.KnownContentExtensions()`, today just `.mdx`) prints a stderr note instead, naming the count, the skipped extensions and the two remedies. Both leave the exit code unchanged, and the note is suppressed when zero files matched so only one message is printed.
- **The embedded HTML template is `internal/report/templates/report.html`** — the `go:embed` path is relative to the report package.
- **Section detection is a per-line header regex**, so a `#` line inside a fenced code block can be misread as a heading; replacing it with goldmark is tracked in #27.

## Direction (see GitHub issues / epics)

- **Positioning:** CI-first staleness *triage* (age-based, section-level, format-agnostic), **not** code↔doc *drift* correctness. Lean into the GitHub Action (#29) and packaging (#30).
- **Tool-agnostic profiles** (#10): Hugo (done) + Mintlify/Starlight/MkDocs/Docusaurus/RST/AsciiDoc/… Starlight is first-class (#18).
- **Agentic drift** (#26): rustydocs does cheap triage; an AI agent verifies real drift on the shortlist (#25). ast-grep stays optional/pluggable (#28).
