# JSON scan contract 2.0

The `version` field is the schema version, independent of the CLI release. A
report is a complete single-run snapshot, not a previous-run comparison or a
review database. No document bodies, remote URLs, credentials or absolute
checkout paths are exported. Keep the working tree stable during a scan; Git
metadata and content are collected during analysis, without pinning HEAD.

## Fields

- `version`: `"2.0"`.
- `generated_at`: the UTC RFC3339 analysis instant. Classification and all ages
  use this same instant. `tool` contains nullable `version` and `build_revision`.
  CLI display defaults `dev` and `none` mean unavailable here and become null;
  known module/release versions and revisions (including `-dirty`) are retained.
  This does not change the human-readable `--version` output.
- `repositories`: objects with `id`, `path` (root relative to the scan anchor),
  `revision`, `dirty`, `shallow`, and `status` (`available` or `partial`). Unknown
  revision/dirty/shallow values are null. Non-repository paths use null
  `repository_id` and a `repository_unavailable` diagnostic.
- `config`: effective profile, auto-detection, extensions, threshold/tier settings,
  exclusions, default-exclusion switch, file/paragraph mode, project/content
  locations, reusable directory/patterns/extensions, resolver and import-map flag.
  Locations have nullable `repository_id` and `path`; an unset directory is null.
  `git_last_modified` records the optional accelerator setting (default false),
  not whether it succeeded; unsupported or unusable results fall back to Git log.
- `coverage`: `analyzed_files`, `failed_files`, `excluded_files`,
  `git_ignored_files`, `extension_skipped_files`, `default_pruned_directories`,
  `excluded_directories`, plus `skipped_extensions` and `pruned_directory_names`.
- `summary`: total/stale files and sections, percentages, fresh/unknown sections,
  files missing history and reusable count. Files are grouping containers; they
  have no dates or age scores.
- `files`: every scanned file, with `id`, repository-relative `path`,
  content-relative `content_path`, nullable `repository_id`, `analysis_status`
  (`analyzed` or `failed`), `history_status` (`available`, `partial` or `missing`),
  section counts and `sections`.
- Each section has `id`, `title`, `heading_path`, `start_line`, `end_line`,
  `own_last_change`, `effective_last_change`, nullable integer `age_days`,
  `is_stale`, `severity`, `content_fingerprint`, `dependency_fingerprint`,
  `freshness_sources` and `dependencies`.
- Change evidence contains nullable `date`, sorted unique `authors` and `commits`.
  Own evidence is the latest committed blame line(s); effective evidence includes
  all tied sources, with each source identified as `own` or `reusable` and a
  nullable `reusable_id`. Snippet authors are never attributed to the prose.
- Dependencies retain `reference`, one-based `line` and byte `column`, `status`
  (`resolved`, `partial`, `skipped`, `unresolved`), and sorted `reusable_ids`.
  `partial` retains permitted evidence when other supporting references were
  rejected or unavailable. The consuming file's `history_status` becomes
  `partial` unless its own history is already `missing`. A resolved
  existing file with missing history remains in the inventory and is referenced,
  even if the legacy resolver labels the reference `unresolved`. Imports attach
  to rendered symbol uses; unused declarations do not create sections. Supporting
  files already traced by the resolver (Hugo templates/data) are included. This
  does not add recursive Markdown includes or dynamic MDX execution.
- `reusables`: deduplicated file inventory with `id`, nullable `repository_id`,
  `path`, `last_change`, nullable `age_days`, `severity`, `content_fingerprint`,
  `resolution_status` and `history_status`. Inventory identity is by repository
  and path, not by the symbol used on a page.
- `diagnostics`: sorted objects with `code`, `severity`, `message`, nullable
  `repository_id`, `file_id`, `section_id`, `reference` and `line`. Codes include
  `history_missing`, `blame_failed`, `uncommitted_content`, `file_read_failed`,
  `reusable_unresolved`, `reusable_history_missing`, `reusable_support_rejected`,
  `reusable_support_missing`, `shallow_history`,
  `repository_unavailable`, `repository_metadata_unavailable`, `zero_matched_files`,
  `extension_skipped`, `scan_exclusions` and `config_warning`.

All collections are arrays, including empty collections. Null dates and ages
mean unavailable, never zero-day freshness. Missing dependency evidence can
coexist with a known own date: diagnostics qualify coverage; they do not change
the existing max-date arithmetic. Shallow blame remains dated but is explicitly
qualified by a warning. Uncommitted blame lines are excluded from own change
history, and produce a partial-history warning; fingerprints describe the
working-tree content, including uncommitted edits.

Supporting paths are authorized before template reads, Git queries, tracing and
fingerprinting, after resolving symlinks on both target and scope. Hugo templates,
themes, `readFile`, partials and `.Site.Data` stay within the selected project
root. The explicitly configured legacy reusable directory permits legacy lookups
only; it never expands Hugo's template-derived scope. In-root symlinks and
explicitly selected symlinked roots are supported; discovered external theme
symlinks are rejected. Git membership and available history grant no permission.
Rejected targets never enter the reusable inventory or contribute dates/hashes.
Diagnostics retain the consuming file, section, reference and line, plus portable
supporting-reference spelling; absolute supporting references are redacted.

## Arithmetic

Effective date is `max(own latest committed line, resolved reusable dates)`.
`is_stale` is true exactly when the date is strictly before
`generated_at - threshold_days * 24h`; equality is fresh. Age truncates the signed
elapsed duration to whole 24-hour days. Severity uses the configured warning,
caution and critical tiers (inclusive boundaries); a date can have severity
`warning` while `is_stale` is false at the exact reporting boundary. Missing dates
have severity `unknown` and `is_stale: false`, meaning unassessed, not fresh.
File-only mode intentionally analyzes no sections; it exports the file's history
status and the mode flag. Consumers must not interpret that mode as a complete
section inventory.

## Portability, identity and fingerprints

The scan anchor is the content directory's enclosing Git root, or the project
root when there is no enclosing repository, or the content directory. Repository
IDs hash the root's slash-separated path relative to that anchor. The main root
therefore has path `.`. Nested and external repositories use their relative
locations. Copying the same relative repository/content/dependency layout to
another runner preserves IDs; moving external roots relative to the scan does
not. Paths outside a repository are content-relative and explicitly unassociated.
Remotes are omitted. Scope historical comparisons to your own project identity.

IDs use full SHA-256 digests prefixed `repo:`, `file:`, `section:` or `reusable:`.
The file/reusable basis is `[repository_id, path]` encoded as compact JSON.
The section basis is `[file_id, logical_key]`; the logical key is compact JSON
heading hierarchy plus chunk kind and a one-based duplicate occurrence. Line
numbers do not enter identity. Paragraph identities use their heading context and
occurrence, not their displayed `(L...)` suffix. Renames, changed heading context,
reordered duplicates or inserted paragraphs may change identity. No rename
tracking is provided.

`content_fingerprint` is `sha256:` plus the hexadecimal SHA-256 of UTF-8 content:
CRLF becomes LF; leading/trailing blank lines and terminal newlines are removed;
all internal whitespace is retained. Section fingerprints include owned source
(including literal include syntax), without imported declaration boilerplate or
dependency bodies. Reusable fingerprints cover the whole supporting file.
`dependency_fingerprint` separately hashes compact JSON sorted unique pairs of
`[reusable_id, content_fingerprint]`. It does not mix dependency changes into the
own-content hash. Unresolved/skipped relationships remain in `dependencies` for
consumers that need to compare resolution state as well.

Files/reusables/repositories sort by ID; sections stay in source order;
references sort by location; source IDs, authors and commits sort lexically;
diagnostics sort lexically by their compact serialized JSON, with fields ordered
`code`, `severity`, `message`, `repository_id`, `file_id`, `section_id`, `reference`,
`line`. Serialized `line` values compare lexically, not numerically. Repeated
export of an unchanged in-memory analysis is byte-for-byte deterministic.

## Coverage definitions

`analyzed_files + failed_files` equals `summary.total_files`. Excluded files count
visited allowed-extension files rejected by configured file patterns. Git-ignored
files count allowed-extension files surviving directory and file exclusions.
Extension-skipped files count visited known documentation extensions outside the
active allowlist, surviving user file exclusions. Files below pruned directories
are never visited and do not enter any file counter. Directory counters count
pruning decisions, not the number of descendants. Other non-document assets are
not counted. Scan traversal/configuration failures that prevent a result remain
CLI errors; read/history failures that permit a result are structured diagnostics.
Partial supporting evidence does not change file coverage counters: a successfully
analyzed file remains analyzed, and rejected supporting files are not scanned
files. Fresh/stale counts retain the permitted max-date calculation; inspect
dependency status, file history status and diagnostics to assess completeness.

## Migration from 1.0

Read `files[].sections` as a complete inventory; filter `is_stale` for the old
stale-only view. Replace `last_updated`, `days_stale`, `author` and `level` with
explicit change evidence, nullable `age_days` and `severity`. Use
`own_last_change` for prose attribution and `freshness_sources` to explain snippet
freshness. File `last_updated`/`days_stale` and summary `oldest_file`/`oldest_days`
are removed. Reusables use stable IDs and paths instead of capture names. Replace
absolute `config.content_dir` with its location object. Always check history,
diagnostics and coverage before calling a run clean. Historical comparisons,
review decisions, scheduling, persistence and dashboards belong to consumers.

See [the example artifact](examples/scan-v2.json).
