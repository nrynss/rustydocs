# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/nrynss/rustydocs/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/nrynss/rustydocs/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/nrynss/rustydocs/releases/tag/v0.2.0
