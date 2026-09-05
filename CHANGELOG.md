# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Documentation-tool **profiles** (`internal/config/profile.go`), the scaffold
  for epic #10. A profile supplies the content extensions, project-root
  markers and reusable-reference patterns (plus a `Resolver` field that is
  forward scaffolding for #7 and does not change behaviour yet); explicit user
  settings always win. Two built-ins: `markdown` (`.md`/`.markdown`, no include
  mechanism, reusable detection off — the default) and `hugo` (`.md`/
  `.markdown`/`.mdx`, shortcode + MDX component detection). Select with
  `--profile NAME` / `"profile"` in `config.json`, or leave empty to
  auto-detect: a `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}`
  file, or a `config/_default/` Hugo config (`hugo.*` or `config.*` under it)
  at or above `content_dir` — or an explicit `hugo_root` — selects `hugo`,
  otherwise `markdown`. The config-file and `themes/` markers keep detection
  working on a fresh clone of a site whose layouts come from a theme, where git
  has not recreated an empty `layouts/` directory. `--list-profiles` prints the
  built-ins and the run banner shows the resolved profile, e.g.
  `Profile: markdown (auto-detected)` (#11).
- `config.DetectRoot(contentDir, markers)` finds the nearest ancestor holding
  any marker, so upcoming profiles (Mintlify `docs.json`/`mint.json`, #7) can
  reuse it; it replaces the Hugo-only root finder. Markers carry a kind: a
  name ending in `/` (`layouts/`) must be a directory, any other name must be
  a regular file, so a stray file named `layouts` never selects `hugo`; a
  marker may be a relative path (`config/_default/hugo.toml`), in which case
  the kind rule applies to its last segment.
  Profile auto-detection uses the same walk across all profiles at once, so
  the marker nearest to `content_dir` wins regardless of registry order (#11).
- A run that scans zero files now prints a stderr warning naming the resolved
  profile and the extensions in force, and pointing at `--extensions` /
  `--profile` (`--list-profiles`); when the extensions did match but
  `exclude_dirs` / `exclude_patterns` dropped every file, the warning says so
  instead. The exit code is unchanged (#11). The allowlist is attributed to the
  profile only when it *is* the profile's own list: an `--extensions` override
  reads "the configured extensions", and a list that matches neither (the
  legacy `--reusables-dir` flow widens it with the hugo profile's extensions)
  reads "the active extensions", with the profile named separately (#11).
- A run that scans *some* files but skips others now prints a stderr note. A
  file whose extension is documentation under another built-in profile (today:
  `.mdx`) but not under the active allowlist is counted, and the note names the
  count, the extensions involved, the profile in force and the two ways to
  include them (`--extensions`, `--profile`). Files that are documentation
  under no profile (images, `.txt`, `.json`) and files the exclusions would
  drop anyway are not counted, and the note is suppressed when zero files were
  scanned — the existing zero-files warning already explains that case. The
  exit code is unchanged. This is the mixed `.md`/`.mdx` tree with no Hugo
  marker, previously silent (#11).
- The JSON report's `config` object now records `profile` (the resolved profile
  name), `profile_auto` (`true` when it was auto-detected rather than passed
  with `--profile`) and `content_extensions` (the allowlist actually scanned),
  so a CI consumer can tell a clean run from one that never looked at the files
  it cared about (#11). `ApplyProfile` canonicalises the allowlist in place
  (lowercased, dot-prefixed, de-duplicated), so `--extensions mdx` reports
  `.mdx` rather than the raw input, and the config, the walk, the banner, the
  warnings and the report all agree (#11).
- pkg.go.dev badge and a note in the README that `go install` builds report the
  module version via Go build info, with the commit additionally shown for
  builds made from a local clone without ldflags (#39).

### Changed

- Plain-Markdown repositories (no `layouts/` or `themes/` directory, no
  `hugo.{toml,yaml,json}` file and no `config/_default/` Hugo config above
  `content_dir`) no longer get Hugo shortcode / MDX component reusable
  detection by default, and `.mdx` files are only analyzed under the `hugo`
  profile. Select `--profile hugo`, set `hugo_root`, or configure
  `reusables.patterns` to turn detection back on; Hugo sites that match none
  of these markers must pass `--profile hugo` or set `hugo_root` to be treated
  as Hugo. Pointing at a reusables directory (`reusables.dir`, `reusables_dir`,
  `--reusables-dir`) without patterns still restores the Hugo defaults that
  flow was built for — the pattern list *and* the Hugo content extensions, so
  `.mdx` files keep being analyzed — while explicit `content_extensions` /
  `--extensions` and `reusables.patterns` still win.
  `DefaultConfig`/`LoadConfig` no longer bake in
  extensions or patterns; `Config.ApplyProfile` fills them, and the analyzer
  calls it for configs built directly (#11).
- An uncompilable `reusables.patterns` entry now fails the run with an error
  naming the pattern instead of silently falling back to the Hugo pattern set
  (#11).

### Fixed

- Hugo shortcodes provided by a **theme** now resolve. Only
  `<hugo_root>/layouts/shortcodes` was searched, so on a site whose layouts come
  from a theme (the shape the `themes/` marker detects) a shortcode was detected
  but never traced and its section was reported as *unknown*. Each
  `themes/<theme>/layouts/shortcodes` is now searched too, after the project's
  own `layouts/`, which keeps Hugo's lookup order (a project template of the
  same name still wins). Symlinked theme directories are included — the usual
  local theme-development layout (`themes/mytheme` → a checkout elsewhere)
  resolves, and a broken symlink is skipped silently (#11).
- Profile auto-detection no longer walks out of the repository. The upward
  marker search stopped only at the filesystem root, so a checkout living under
  any directory named `themes/` or `layouts/` was misdetected as a Hugo site
  rooted outside it. The walk now stops at the directory holding `.git` — a
  `.git` directory, or a `.git` file pointing at a linked worktree
  (`.git/worktrees/…`); that level is still searched, so a marker next to
  `.git` matches. A **submodule** checkout is not a bound: its `.git` file
  points into the parent repository's `.git/modules/`, and the walk continues
  up into that parent, so a Hugo site whose `content/` is a submodule is still
  detected from the `layouts/` and `hugo.toml` one level above it. A `.git`
  file that cannot be read or parsed stops the walk (conservative). With no
  repository above `content_dir` the walk reaches the filesystem root as
  before. The bound applies to both auto-detection and `DetectRoot` (#11).
- `--version` no longer prints `rustydocs dev` for binaries installed with
  `go install github.com/nrynss/rustydocs/cmd/rustydocs@latest`; it reports
  the module version from `runtime/debug.ReadBuildInfo`. Builds made from a
  local clone without ldflags (`go build ./cmd/rustydocs`) additionally fall
  back to the embedded VCS info for the commit (12-char revision, with a
  `-dirty` suffix for modified trees). The build date is deliberately not
  taken from build info (`vcs.time` is the HEAD commit time, not the build
  time) and is still reported only when set via ldflags. Module-proxy installs
  carry no VCS info, so they show only the version. Release binaries keep the
  ldflags values, which always take precedence (#39).

### Removed

- `config.DetectHugoRoot` — superseded by the general
  `config.DetectRoot(contentDir, markers)`, which any profile can use with
  its own root markers. Callers wanting the previous behavior pass
  `[]string{"layouts/"}` (#11).

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
- `.golangci.yml` (golangci-lint v2) with an **errcheck** policy plus `govet`,
  `ineffassign`, `staticcheck`, and `unused`. errcheck excludes best-effort
  console writes (`fmt.Print*`/`fmt.Fprint*`); the `QF1001`/`QF1012` quickfix
  opinions are disabled. Run with `golangci-lint run ./...`.
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
