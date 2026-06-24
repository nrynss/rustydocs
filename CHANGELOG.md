# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.0] - 2026-06-24

### Added

- Comprehensive test coverage across every package — analyzer, parser, config,
  report, and the CLI — raising total statement coverage from ~29% to ~83%
  (each package now ≥80%), with the staleness/date math fully exercised (#53).
- `internal/testutil` test helper: builds a temporary git repository and commits
  files with controlled author/committer dates, so blame/log timestamps and
  staleness results are deterministic in tests.
- Committed test fixtures under `internal/testutil/testdata/` — a sample Hugo
  site (Markdown/MDX docs, a shortcode with a traced `readFile` data dependency,
  and reusables) that the parser and analyzer tests load via the `testutil`
  helpers (`ReadFixture`, `CommitTree`).
- Reports surface a `files_missing_history` count (JSON summary, Markdown, and a
  highlighted note in HTML), and the CLI prints a stderr warning when files
  cannot be assessed.

### Fixed

- Stale sections are no longer classified `fresh`. `threshold_days` and
  `staleness_levels` were applied independently, so e.g. `--threshold-days 30`
  with the default `warning: 90` reported a 45-day-old section as stale **and**
  labeled it `fresh`. `Config.Normalize` now clamps the warning tier to the
  threshold (tiers stay monotonic) (#54).
- Files with no git history (uncommitted, shallow clone, or not a git
  repository) are reported as **unknown** rather than silently passing as fresh;
  a stderr warning and a summary count make a misconfigured checkout visible
  (#55).
- An unknown section date now renders consistently across formats — `Unknown` /
  `—` with an `unknown` level — instead of `0` days in Markdown but a fabricated
  `999` days mislabeled `critical` in HTML (#56).
- The progress-reporter goroutine in `AnalyzeWithProgress` is now joined before
  the function returns, so its output can no longer race a caller writing to the
  same stream (caught by `go test -race`).
- `git.GetFileLastModified` resolves symlinks before computing the path relative
  to the git root, so a tracked file reached through a symlinked working tree
  (e.g. macOS `/var` → `/private/var`, or a symlinked checkout) is no longer
  reported as having no history.

### Changed

- The CLI entry point is split into a reentrant `runArgs(argv, stdout, stderr)`
  with its own `FlagSet`, so the full pipeline is testable; behavior is
  unchanged.

## [0.3.0] - 2026-06-22

### Added

- Configurable documentation-extension allowlist: analysis is restricted to
  `content_extensions` (default `.md`, `.markdown`, `.mdx`), also settable with
  the `--extensions` flag. Previously every non-binary file was analyzed,
  producing false positives on source, config, and dotfiles (#1).
- CI now builds and tests on Linux, Windows, and macOS (#2).
- Community-health and contributor docs: a tracked `CLAUDE.md`, this
  `CHANGELOG.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, issue/PR templates, and
  Dependabot configuration.
- Test coverage across all packages — analyzer, parser, git, config, report (#4).

### Fixed

- Reports use forward-slash paths, so HTML sidebar navigation works and
  JSON/Markdown paths are portable on Windows (#22).
- CRLF line endings no longer leave a trailing carriage return in section
  titles (#5).
- `git blame` commit hashes are parsed from real `--line-porcelain` output; the
  field was previously always empty (#21).
- A documentation line longer than 64KB no longer aborts blame for the whole
  file (#23).
- Directory/pattern exclusion matches on path-segment boundaries, so excluding
  `docs` no longer also excludes `mydocs/` (#3).
- Markdown report table cells escape pipes and newlines, and section titles are
  truncated rune-safely (no broken multi-byte characters) (#24).
- `GetGitRoot` caches and returns its error on repeated calls (#6).

### Changed

- All GitHub Actions are pinned to commit SHAs for supply-chain safety.

### Removed

- Dead code: the unused root `templates/report.html` duplicate, the unused
  `trimKnownExt` helper, and the binary-extension blocklist superseded by the
  allowlist (#6, #1).

## [0.2.0] - 2025

Earlier releases predate this changelog; see the
[git history](https://github.com/nrynss/rustydocs/commits/main) and
[releases](https://github.com/nrynss/rustydocs/releases).

[Unreleased]: https://github.com/nrynss/rustydocs/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/nrynss/rustydocs/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/nrynss/rustydocs/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/nrynss/rustydocs/releases/tag/v0.2.0
