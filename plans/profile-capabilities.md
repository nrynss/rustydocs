# Profile capability refactor plan

## Scope and final design

Preserve the behavior of the five existing profiles (`markdown`, `gitbook`,
`hugo`, `mintlify`, `starlight`) while replacing tool-identity gates with
capabilities. Deliver five separately reviewed phases in one PR on
`codex/profile-capabilities`, based on `main` at `d0f6dca`.

Profiles composing **existing** capabilities can be added as registry data,
except when detection needs a new Go marker predicate and binding. New include
or resolution semantics require generic parser code and tests. The parser must
not branch on profile names; future profiles can still require parser edits.

Keep the shared parser package. No new profiles, goldmark, TOML, runtime
profile-loading API, external dependencies, or user-config format changes are
part of this work.

`Profile.ParserCapabilities` is a narrow value with no `Name`, `Resolver`, or
`ImportMap`. The analyzer passes it as `ReusableConfig.Capabilities`, alongside
the existing authoritative `Resolver` and `ImportMap` fields and the effective
user-configured `Patterns` and `Extensions`. It does not pass the whole `Profile`.
`IncludeExample` is CLI display metadata and never reaches the parser.

## Five phases

1. **Fields and defaults.** Add `ParserCapabilities`, `PathBaseMode`, and
   `IncludeExample`; populate existing profiles without changing behavior.
   Clone capability slices on registry lookup, preserving nil versus empty.
2. **Consume capabilities without identity.** Replace parser name comparisons
   with capability reads and CLI example selection with `IncludeExample`.
   Remove profile-name transport, preserve effective overrides, and clone
   parser input slices. Add capability-independence tests without changing
   existing behavior expectations.
3. **Separate responsibilities by pure moves.** Keep chunking in
   `internal/parser/markdown.go`; move reusable detection, path/Hugo lookup,
   and resolution into `reusable.go`. Keep the existing MDX `importmap.go` layer.
   Marker algorithms remain in `config/profile.go`.
4. **Embed validated definitions.** Store one JSON definition per built-in under
   `internal/config/profiles/`, with explicit `registry.json` precedence.
   `profile_loader.go` uses private wire types and standard-library `go:embed`;
   predicate algorithms and bindings remain Go. Invalid assets fail at package
   initialization as contextual developer errors. Test schema, parity, cloning,
   and precedence.
5. **Document the contract and validation.** Update this plan, architecture
   guidance, contributor instructions, and Unreleased changelog. Each phase
   receives independent review and remediation before the next phase.

## Capability contract

| Capability | Preserved behavior |
|---|---|
| `MaskFencedChunking` | Hide fenced headings/references during chunking while retaining nonblank blame lines. GitBook enables it. |
| `SkipFencedCaptures` | Ignore reusable captures inside fences. GitBook and Starlight enable it independently of chunk masking. |
| `SkipURLCaptures` | Skip captures containing `://`. GitBook enables it. |
| `SkipAliasShapedIncludes` | Skip unresolved alias-shaped **include** captures; component provenance is unaffected. Starlight enables it. |
| `PathCapturesOnly` | Direct path lookup before resolver dispatch, without legacy directory, shortcode, or cached-name fallbacks. GitBook enables it. |
| `StripCaptureFragments` | Strip anchors and queries before direct path lookup. GitBook enables it. |
| `AllowedExplicitPathExtensions` | Restrict extensions written in captures by exact, case-sensitive equality. GitBook uses `[".md"]`. |
| `PathBaseMode` | Zero value `""` means shared-first; `"page-only"` means page directory for all non-root-absolute captures. GitBook uses page-only. |
| `IndexFileNames` | Extensionless directory entry stems; nil defaults to `["index"]`. GitBook uses `["README", "index"]`. |

Flags default to false. An omitted or JSON `null` extension allowlist means
nil/unrestricted; `[]` rejects every explicitly written extension.
Extensionless captures remain eligible and try effective reusable extensions,
including user overrides. GitBook's explicit `.md` restriction does not become
a broader content-extension test and does not accept `.MD` or `.mdx`.

An omitted or `null` index list defaults to `["index"]`; `[]` disables directory
index candidates. An extensionless capture tries the literal path, then each
extension-suffixed path, then directory entries ordered by **extension first,
index name second**. With extensions `[".md", ".mdx"]` and GitBook's names,
index candidates are `README.md`, `index.md`, `README.mdx`, `index.mdx`.
Both capability slices are cloned at registry lookup and parser construction,
preserving nil versus empty and preventing mutation of shared defaults.

Root-absolute captures always use the project root. Shared-first bare captures
try `snippets/`, `_snippets/`, root, then page directory; explicit `./` or `../`
captures try page directory before shared bases. Page-only mode applies before
those shared fallbacks. Root containment and exact path-case checks are unchanged.

Fence masking and capture skipping stay independent. Alias skipping still
requires include-pattern provenance and failed path resolution; a missing
relative partial remains unresolved. `PathCapturesOnly` does not remove legacy
`reusables_dir` fallback for other path-resolver profiles.

## Embedded definitions and boundaries

Registry order is `markdown`, `gitbook`, `hugo`, `mintlify`, `starlight`.
Nearest marker wins; only same-directory ties use this order. Public profile
listing stays alphabetically sorted. Definitions are compiled into the single
binary, not installed/read as external runtime config files. The private
snake_case manifest schema does not change public user-config JSON.

Validation rejects unknown/duplicate JSON fields, trailing data, wrong types,
missing/unlisted assets, name mismatches, invalid/duplicate list values,
unsupported resolver/base modes, invalid regexes or capture counts, and unknown
predicate bindings. Markdown fallback must exist. Predicate names bind Go
functions and listed file markers; directory markers cannot have predicates.

Keep the legacy Hugo default shim and compatibility lookups in Go.
`patternExtensionConstraints` remains keyed by exact regex string, and equality
with `config.MDXComponentPattern` determines component provenance. A user
pattern identical to a built-in inherits those semantics. Revisit separately
if the same pattern needs different constraints or another component-usage
pattern is introduced.

## Future profile work (not implemented here)

- **Docusaurus (#13):** relative MDX imports can compose existing path/import-map
  behavior; a new package marker predicate would live in Go. A follow-up must
  validate support and corpus behavior; this sketch is not a shipped profile.
- **MkDocs (#12):** [PyMdown Snippets documentation](https://facelessuser.github.io/pymdown-extensions/extensions/snippets/)
  specifies current working directory as the default snippet base and supports
  ordered `base_path` locations. It processes snippets inside fences. Root-only
  lookup would be an assumption, not a safe superset of upstream resolution.
  New base semantics/configuration handling belong in a separate implementation;
  this refactor does not prove MkDocs is data-only.
- **VitePress (#15):** [Markdown inclusion](https://vitepress.dev/guide/markdown#markdown-file-inclusion)
  supports `<!--@include: ./parts/x.md-->` and `@`-prefixed paths whose source root
  follows `srcDir`. Root-alias resolution is future generic capability work,
  distinct from Starlight's skip-unresolved-alias policy. `<<<` code snippets
  bypass the MDX import map, so its `.ts` skip rule says nothing about their
  handling. Code freshness/drift and source-root semantics need separate
  follow-ups; no VitePress support is claimed here.

## Validation gate and record

- Preserve existing resolution, profile, Starlight, GitBook, and integration
  expectations; adapt construction sites mechanically. Additional tests pin
  independent fence gates, provenance, fallback ordering, exact extensions,
  overrides, renamed/synthetic capabilities, cloning, manifest strictness/data
  parity, and actual marker tie precedence.
- Run `go build ./...`, `go test ./...`, `go test -race ./...`, `go vet ./...`, and
  `gofmt -l .` (empty), plus at least 80% coverage per production package.
  `internal/testutil` is a test-only helper and exempt from the coverage floor.
- Search production parser/CLI code for built-in profile constants. The only
  retained parser identity lookup is the pre-profile Hugo default shim; CLI
  diagnostics consume resolved display metadata.
- Compare baseline `d0f6dca` and refactor output with identical flags and pinned
  corpus revisions. Fix clocks in tests or run paired contemporary CLIs;
  normalize **only generated report metadata**, never age/staleness values.
  Compare Markdown, HTML, JSON, stderr, and `--list-profiles` output.

The corpus gate covered 20 cases: five profiles in section/paragraph/file modes
(15), legacy reusable-directory cases for GitBook/Mintlify/Starlight (3), and
two real trees with automatic detection (2). The temporary synthetic fixture
commit was `3363f9d037c58fc3713bfc2dcc67d89ab8fc68be` (January 1, 2025, 12:00 UTC),
with an untracked GitBook draft. It covered includes/imports, fenced examples,
alias/unresolved references, extensionless README lookup, and missing history;
this local fixture and its runner are not shipped repository assets. Real
corpora were `lambo/site` at `e11fb06ba61a480c8400d89ad6e40bbc39fc46c0` and
`mooshik/docs` at `47d4e91848047584b587b58e4bc26d7323dc2c5c`.

An early saved baseline and later phase-4 run straddled the fixture's 24-hour
age boundary, changing an age from 641 to 642 days. A paired rerun passed all
20 cases after generated timestamps were normalized, with exact stderr and
profile-list equality. Uncontrolled, separately timed runs must not be called
literally byte-identical. Final build/tests/race/vet/formatting/coverage gates
passed; production-package coverage was 92.3–94.4%.
