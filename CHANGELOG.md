# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A **`mintlify` profile** and the **direct-path resolver** behind it (#7).
  Mintlify projects are auto-detected from a `docs.json` (current) or
  `mint.json` (legacy) file at or above `content_dir`; the profile scans `.md`
  and `.mdx`, and detects exactly one kind of include —
  `<Snippet file="aws-config.mdx" />`, in either quote style
  (`file="…"` and `file='…'`), with `<SnippetGroup …>` deliberately excluded.
  Unlike the Hugo patterns, which
  capture a component *name*, this one captures the snippet **path**, which the
  new `ResolverPath` resolves directly, the way Mintlify itself does: a capture
  starting with `/` against the project root; one starting with `./` or `../`
  against the directory of the referencing page first; and a bare filename or
  bare relative path — the form real Mintlify projects overwhelmingly write —
  against the project's snippets directory (`snippets/`, then `_snippets/`),
  then the project root, and only then the referencing page's directory as a
  tolerant last resort. A name present both in `snippets/` and next to the page
  resolves to `snippets/`. `.mdx`/`.md` (and `index.*`) are tried when the
  capture has no extension, and the first candidate that exists wins. A path that leaves the project root is ignored — containment is
  checked after resolving symlinks on both the candidate and the root, so
  neither a `..` traversal nor a symlink inside the tree pointing elsewhere can
  pull a foreign file into a report — and a reference that resolves
  to nothing stays *unknown* rather than becoming fresh. The resolved snippet's
  commit date folds into the section that includes it, exactly as Hugo
  shortcodes do, and the reports name it by its path relative to the project
  root, so two pages' same-named relative snippets stay distinct rows. A bare
  `<Card />` or `{{< shortcode >}}` on a Mintlify page is
  deliberately **not** a reusable; imported snippets (`import X from
  '/snippets/x.mdx'` used as `<X />`) need an import map and remain a follow-up
  (#13/#18/#19). Select with `--profile mintlify` / `"profile": "mintlify"`, or
  let auto-detection find it: the nearest marker wins, so a Mintlify docs tree
  nested inside a repo that also has a Hugo `layouts/` selects `mintlify`. The
  `docs.json` / `mint.json` markers are validated by content as well as by
  name — the file must parse as a JSON object carrying a recognisably Mintlify
  key (`navigation`, `theme`, `colors`, `logo`, `favicon`, `tabs`, `anchors`,
  or a `$schema` mentioning Mintlify) — so an unrelated or malformed
  `docs.json` never selects the profile. The check streams the document and
  reads at most 1 MiB of it, so a huge or hostile file cannot be turned into a
  large allocation just by naming it `docs.json`. When a Hugo and a Mintlify marker sit
  in the *same* directory, `hugo` wins the tie: `layouts/` and `hugo.toml` are
  the stronger evidence, and downgrading such a site would silently switch
  shortcode tracing off. Pass `--profile mintlify` to override that.
- A **`--project-root PATH`** flag and its `"project_root"` config key, naming
  the directory reusable references resolve against (the Hugo site root, the
  Mintlify docs root). It is the profile-neutral replacement for `hugo_root` —
  see *Changed* for the rename and the deprecation (#7).
- A stderr **note whenever reusable references produced no resolved history**,
  naming the profile, counting them and naming the first three captures (then
  "and N more"): those references are reported *unknown*, and a run that
  produced a report full of *unknown* used to exit 0 with an empty stderr. It
  is gated on the count (`Results.UnresolvedReusables`), so a run whose
  includes all resolve — including through a legacy `reusables_dir` — and a run
  with no reusable references at all stay quiet. It is also scoped to the
  direct-path resolver (`mintlify` today), where the capture *is* a path and
  every failure is a real defect; under `hugo` the profile's generic
  MDX-component pattern captures every capitalised tag (`<Tabs>`, `<Card>`,
  `<Badge>`), so the note's population there was overwhelmingly noise — those
  rows still appear in the report as level `unknown`, which is where a Hugo
  user should look. The wording covers both ways a reference lands here — the
  file is missing, or it exists but has never been committed — because the
  second is just as common and the report meanwhile lists it under its correct
  resolved path. When the cause is a project root that was never found (the
  classic case being `--profile mintlify` on a tree with no `docs.json` /
  `mint.json`) the note adds a sentence naming the markers that were looked for
  and `--project-root` as the remedy. The exit code is unchanged (#7).
- A stderr **note when a supplied project root is not used by the resolved
  profile** — it has no root markers, or resolves no reusable references at
  all. The root is validated and then never read, and the banner deliberately
  omits it in that case, so the run used to say nothing at all. This is the
  likeliest migration error the `project_root` rename creates: a markerless
  Hugo site whose config said `hugo_root` used to get the `hugo` profile (that
  key selected it) and with it shortcode tracing; migrated to `project_root` it
  gets `markdown`, no tracing, and — because the `.md` files still match — a
  run that looks entirely healthy. The note points at `--profile`. The exit
  code is unchanged (#7).
- **A candidate root marker that cannot be read is now reported.** A
  `chmod 000 docs.json` silently downgraded a real Mintlify project to the
  `markdown` profile, because the content predicate could not tell "unreadable"
  from "not a Mintlify config". Detection is unchanged — an unreadable marker
  is skipped — but the reason is collected on `Config.Warnings` and printed to
  stderr after the banner. The warning is scoped to the marker, not to the
  profile: a readable `mint.json` beside an unreadable `docs.json` still
  selects `mintlify`, and the warning simply records the marker that was
  skipped. A `docs.json` that reads fine and simply is not a Mintlify config
  stays silent: that is not an error (#7).
- Documentation-tool **profiles** (`internal/config/profile.go`), the scaffold
  for epic #10. A profile supplies the content extensions, project-root
  markers and reusable-reference patterns (plus a `Resolver` field, wired up in
  #7 below); explicit user settings always win. Two built-ins: `markdown` (`.md`/`.markdown`, no include
  mechanism, reusable detection off — the default) and `hugo` (`.md`/
  `.markdown`/`.mdx`, shortcode + MDX component detection). Select with
  `--profile NAME` / `"profile"` in `config.json`, or leave empty to
  auto-detect: a `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}`
  file, or a `config/_default/` Hugo config (`hugo.*` or `config.*` under it)
  at or above `content_dir` selects `hugo`, otherwise `markdown`. The config-file and `themes/` markers keep detection
  working on a fresh clone of a site whose layouts come from a theme, where git
  has not recreated an empty `layouts/` directory. `--list-profiles` prints the
  built-ins and the run banner shows the resolved profile, e.g.
  `Profile: markdown (auto-detected)` (#11).
- `Profile.DetectRoot(contentDir, warn)` finds the nearest ancestor holding any
  of that profile's markers, so every profile (Mintlify `docs.json`/`mint.json`
  included, #7) locates its root the same way; it replaces the Hugo-only root
  finder. Markers carry a kind: a
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

- **Reusable resolution now memoizes its `git log` lookups for the duration of
  a run** (#65). A snippet or shortcode referenced from many pages used to cost
  one `git log` subprocess *per referencing page*: on a 300-page x 3-snippet
  Mintlify tree that was ~900 redundant subprocesses, and process spawn
  dominated the clock: 11.1 s wall / 27.8 s user / 45.5 s sys, against a 2.1 s
  wall floor for the same files with resolution switched off entirely. A
  `git.FileInfoCache` is now created once in `analyzer.AnalyzeWithProgress`,
  before the worker pool starts, and shared by every worker and every file's
  `parser.ReusablePatterns` — a cache on `ReusablePatterns` itself would only
  ever dedupe within one page, since one is built per file. Concurrent lookups
  of the same path collapse to a single subprocess, negative results (no
  history, and git errors) are cached too, and paths are keyed on their
  absolute symlink-resolved form so the several spellings one file arrives
  under share an entry. The same tree now runs in 2.1 s wall / 5.3 s user /
  8.7 s sys — a 5.4x speedup, and within 4% of that 2.1 s no-resolution floor.
  Reports are unchanged: a run analyses one commit state, so every lookup in it
  has exactly one right answer. Nothing is cached across runs or on disk, and
  blame output is not cached.
- **`hugo_root` is renamed `project_root`** (`--project-root`), and the Go
  field `Config.HugoRoot` is now `Config.ProjectRoot`. **Existing config files
  keep working**: `"hugo_root"` is still read as the project root, now with a
  deprecation warning on stderr telling you to rename the key; `"project_root"`
  wins when both are present, and that case gets its own warning saying the
  legacy key was *ignored* and naming the path it pointed at — dropping a stale
  `hugo_root` in silence made it look like it was still in force. The rename is not cosmetic — the old name was
  wired into behaviour. Setting a root used to *force the `hugo` profile*
  during auto-detection, so `--project-root` on a Mintlify repo selected
  `hugo`, whose generic MDX-component pattern captured `<Snippet …>` as the
  component name "Snippet" and resolved nothing. Auto-detection is now driven
  by markers alone and a supplied root never biases it. The one legacy
  behaviour preserved: a config that sets **`hugo_root`** (the deprecated
  spelling), names no profile, and points at a tree with no Hugo marker
  anywhere still gets the `hugo` profile, as it always did. That fallback is
  deliberately *not* extended to `project_root` / `--project-root` (#7).
- **An explicitly supplied project root that does not exist is now a
  configuration error** — a run that used to exit 0 now exits 1.
  `--project-root /does/not/exist` (or `"project_root"` / the deprecated
  `"hugo_root"`) used to be accepted in silence: the banner printed the bogus
  root, nothing resolved, every include was reported *unknown* and the run
  exited 0. The root must now exist and be a directory, and the error names the
  path and the flag or key that supplied it. Check any config whose root is
  stale, or is relative and resolves differently under CI's working directory.
  A symlink to a real directory is fine; a dangling one is not. Auto-detected
  roots are unaffected — detection only ever returns a directory it just
  stat'ed (#7).
- Plain-Markdown repositories (no `layouts/` or `themes/` directory, no
  `hugo.{toml,yaml,json}` file and no `config/_default/` Hugo config above
  `content_dir`) no longer get Hugo shortcode / MDX component reusable
  detection by default, and `.mdx` files are only analyzed under the `hugo`
  profile. Select `--profile hugo` or configure `reusables.patterns` to turn
  detection back on; Hugo sites that match none of these markers must pass
  `--profile hugo` to be treated as Hugo. Pointing at a reusables directory (`reusables.dir`, `reusables_dir`,
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
- Reusable **resolution now follows the resolved profile's `Resolver`** rather
  than being inferred from which roots happen to be set. Previously any
  non-empty project root meant "trace Hugo shortcodes"; now a profile whose
  resolver is `none` (today: `markdown`) does not trace shortcodes even when
  `project_root` is set explicitly *and* `reusables.patterns` is configured by
  hand. That combination is only reachable by also setting
  `"profile"`, which no released version understands, so no existing config can
  hit it; and when it is hit the affected references resolve to *unknown*, never
  to *fresh*. Set `"profile": "hugo"` (or `--profile hugo`) to trace shortcodes
  under a hand-written pattern list (#7).

### Fixed

- Hugo shortcodes provided by a **theme** now resolve. Only
  `<project root>/layouts/shortcodes` was searched, so on a site whose layouts come
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
  before. The bound applies to both auto-detection and `Profile.DetectRoot`
  (#11).
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

- `config.DetectHugoRoot` and its short-lived replacement
  `config.DetectRoot(contentDir, markers)` — superseded by
  `Profile.DetectRoot(contentDir, warn)`, which walks the same way but applies
  the profile's marker content predicates. The marker-list form was a trap: it
  matched on name and kind only, so a caller writing
  `config.DetectRoot(dir, mintlifyProfile.RootMarkers)` would silently let any
  file named `docs.json` select the Mintlify root. Callers wanting a root for
  a profile use `profile.DetectRoot(dir, nil)` (#11, #7).

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
