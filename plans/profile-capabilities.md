# Profile capability refactor plan

## Scope

- Replace every per-tool branch in the parser and CLI with declarative capability fields on `config.Profile`, on a single branch off `main` (`refactor/profile-capabilities`), with **zero behavior change**.
- The litmus test this buys: **adding a profile must require no edits to `internal/parser` or `cmd`** — the profile becomes pure registry data. Verified against the three queued profiles (#12 MkDocs, #13 Docusaurus, #15 VitePress) below; Docusaurus and MkDocs pass as pure data, MkDocs needs one *additive* capability value (a new `PathBaseMode`), VitePress needs one future capability sketched but not built here.
- Explicitly **not** a per-tool package split. The flavours share the entire resolution machinery (`ResolverPath`, `caseExactUnder`, containment, the import map); GitBook, Mintlify and Starlight differ from each other in nine small, nameable ways, and those differences are the refactor's subject — not the package layout.

## Current state (analysis, main @ ac14ff0)

What is already flavour-agnostic and stays untouched: `internal/git`, `internal/report`, `internal/analyzer` (it only hands `cfg.ResolvedProfile` fields to the parser), the import map (`importmap.go` — its bare-specifier and content-extension skip rules already anticipated Docusaurus, see its own comment), and all of `config.go`. `Resolver` is already a strategy dispatch on data (`none`/`hugo`/`path`); it stays an enum.

The flavour knowledge that is *not* data lives in ten sites:

| Site | Tool(s) | Behavior today | Becomes |
|---|---|---|---|
| `markdown.go:328` ParseChunks | gitbook | Fences masked before chunking (blame lines kept, headings/refs hidden) | `MaskFencedChunking bool` |
| `markdown.go:649` FindReusables | gitbook, starlight | Captures inside fenced code are skipped (examples don't render) | `SkipFencedCaptures bool` |
| `markdown.go:665` FindReusables | gitbook | Captures containing `://` are skipped | `SkipURLCaptures bool` |
| `markdown.go:776` ResolveReusable | starlight | Alias-shaped include capture (`@…` or bare extensionless) that failed path resolution is `Skipped`, not unresolved (#74) | `SkipAliasShapedIncludes bool` |
| `markdown.go:846` resolveExisting | gitbook | Direct path only — no legacy reusables-dir / cached-name fallbacks | `PathCapturesOnly bool` |
| `markdown.go:908` resolveDirectPath | gitbook | `#anchor` / `?query` stripped from captures | `StripCaptureFragments bool` |
| `markdown.go:915` resolveDirectPath | gitbook | Target must carry a content extension (`.md`) | `ResolveContentOnly bool` |
| `markdown.go:1005` directPathBases | gitbook | Bare captures resolve against the page's directory only (vs the shared snippets-first bases) | `PathBaseMode` enum: `PathBasePageOnly` vs default `PathBaseSharedFirst` |
| `markdown.go:1043` pathCandidates | gitbook | Extensionless captures also try `README.md` before `index.md` | `IndexFileNames []string` (`["README", "index"]` vs default `["index"]`) |
| `main.go:444` unresolved-note | gitbook, starlight | Per-profile example include string in the stderr note | `IncludeExample string` |

Supporting couplings, and their verdicts:

- `ReusablePatterns.profile` / `ReusableConfig.Profile` (a string carried into the parser purely so the nine branches can compare it): **removed**. `ReusableConfig.Profile` becomes the whole resolved `config.Profile`; `ReusablePatterns` copies out the capability fields it needs. A name the parser cannot compare against is a name it cannot grow new branches on.
- `parser.hugoProfile()` (`markdown.go:288`, feeding the legacy `DefaultReusablePatterns` shim): kept — it is the pre-profile API's default, not a behavioral branch.
- `ApplyProfile`'s three `mustProfile(ProfileHugo)` lookups and the legacy `hugo_root` → hugo fallback: kept as-is. They are legacy-compat *by definition* and live in the registry's own package.
- `patternExtensionConstraints` (global map keyed by pattern *string*) and the provenance rule `p == config.MDXComponentPattern` (`markdown.go:261`): **kept, deferred** — see trade-offs. No queued profile breaks them.

## Approach

Three commits, one PR, each shippable:

1. **Fields + defaults, no behavior change.** Add the capability fields to `config.Profile` (names provisional) with doc comments carrying the rationale from the current branch sites; every existing profile gets its values set so behavior is identical when nothing else changes. `PathBaseMode` gets two values. `IndexFileNames` nil means `["index"]`.
2. **Replace the ten sites.** Each branch becomes a read of the capability field; the conditions' internal logic (provenance via `includeCaptures`, `isPartialAliasShape`, fence scanning, base ordering) is unchanged — only the *gate* changes from tool identity to capability. `ReusableConfig.Profile` switches to the whole `config.Profile`; mechanical test updates follow (tests construct from `LookupProfile(name)` instead of a name string).
3. **File splits, pure moves.** `markdown.go` (1,525 lines) splits into `markdown.go` (chunking: ParseChunks/preamble/paragraphs, lines 1–640) and `reusable.go` (detection + resolution + Hugo lookup, lines 641–1555) — aligning code with the existing test files (`resolution_test.go` etc.). Optionally split the marker predicates out of `config/profile.go` into `config/markers.go`. No code changes, `git log --follow` preserved.

Invariants the implementation must preserve (each currently implicit in a branch comment):

- `MaskFencedChunking` and `SkipFencedCaptures` stay **independent**: starlight wants only the capture-level skip, gitbook both. Do not collapse them into one fence-handling enum.
- `SkipAliasShapedIncludes` keeps its interaction with provenance: it applies only to captures an *include* pattern produced (`fromIncludePattern`), never to component-pattern captures — the #68/#74 rules compose through the flag the same way they compose through the name check today.
- `PathCapturesOnly` gates the early return in `resolveExisting` *before* the resolver dispatch, exactly as the gitbook check does now — it must not silently become "no fallbacks for every `ResolverPath` profile", which would change Mintlify/Starlight behavior when a legacy `reusables_dir` is configured.
- A new `PathBaseMode` value later is additive parser work (one case in `directPathBases`) — that is capability semantics arriving, not tool identity creeping back; the grep gate below still holds.

What the queued profiles then cost — the refactor's payoff, checked against upstream docs:

- **Docusaurus (#13)** — pure data, zero parser edits. `.md`/`.mdx`; markers `docusaurus.config.{js,mjs,ts}` (name alone is unambiguous) plus a `package.json` predicate for the quoted `@docusaurus/core` dependency (the `isStarlightPackage` shape); patterns `[MDXComponentPattern]`; `ImportMap: true`; `ResolverPath`; `SkipFencedCaptures: true`. Its `_partials/` convention is page-relative MDX imports — the import map's explicit-relative resolution already covers it, and `@site/…`/`@theme/…` bare specifiers are already skipped by `isBareModuleSpecifier`.
- **MkDocs (#12)** — pure data plus one new `PathBaseMode` value. Pattern for pymdownx.snippets' `--8<-- "path"` (include pattern, both quote styles); upstream resolves snippets against the `base_path` option, whose default is the config directory — modelled as a root-only base mode (reading `base_path` out of YAML is stdlib-hostile and out of scope; documented limitation, safe direction since root-only ⊇ the common setup). Marker `mkdocs.yml` with a text predicate (a bounded read matching `site_name:`, required by upstream in every config) so a random `mkdocs.yml`-named file doesn't select the profile.
- **VitePress (#15)** — `<!--@include: ./parts/x.md-->` is an explicit-relative path capture and works through today's `PathBaseSharedFirst`. Its `<<< @/snippets/x.ts` embeds *code* — the import map's content-extension allowlist already skips `.ts`, which is the right answer for freshness. If `@/`-prefixed *markdown* includes ever need resolving, that is a new "alias prefix maps to root" capability — the **opposite** of `SkipAliasShapedIncludes` (starlight's rule is skip-because-unresolvable; VitePress's would be resolve-because-documented). Design it at #15; do not stretch the starlight capability to cover both.

## Accepted trade-offs

- **Capability bools over a strategy interface.** Ten fields is at the edge of where an interface would be cleaner, but the fields stay declarative (comparable, printable, registry-defined), need no plumbing per implementation, and every one maps to a real upstream distinction rather than a hypothetical. Revisit toward an interface only if a future capability needs more than a bool/enum/small list.
- **`patternExtensionConstraints` and component-provenance stay string-keyed.** Moving them into per-profile pattern structs would churn `Config.Reusables.Patterns` (user wire format `[]string`) and the "user pattern identical to a built-in inherits its semantics" rule, for no benefit any queued profile needs: Docusaurus reuses `MDXComponentPattern` unconstrained; MkDocs/VitePress have no component usage. Trigger to revisit: a profile needing *different* constraints for the same pattern string, or a second component-usage pattern.
- **The parser loses the profile name entirely** (including for diagnostics). The stderr note reads `cfg.ResolvedProfile.Name` at the CLI, where the name belongs; tests that assert per-profile behavior set capabilities from `LookupProfile`, so they keep pinning behavior, not spelling.
- **`IncludeExample` moves display wording into the registry.** That is the point — the example is per-profile documentation, already de facto maintained in two places.
- **MkDocs `base_path` unread** (above) and **`mkdocs-macros` Jinja includes out of scope** initially: a plugin-dependent include mechanism with no default-on corpus presence is a worse first cut than the snippets extension, which is upstream-documented and common.

## Validation

- The refactor's gate is **zero test-expectation changes**: `resolution_test.go`, `profile_test.go`, `starlight_test.go`, the GitBook cases and `integration_test.go` pin the behavior being relocated; only construction sites change mechanically (commit 2). Any expectation edit during the refactor is a bug in the refactor.
- `go build ./... && go test ./... && go test -race ./... && go vet ./... && gofmt -l .` clean; `go test -cover ./...` keeps ≥80% per package.
- **Grep gate (definition of done):** `grep -rn "ProfileGitBook\|ProfileStarlight\|ProfileHugo\|ProfileMintlify" internal/parser cmd --include="*.go" | grep -v _test.go` returns only the legacy shim's `hugoProfile()` lookup and nothing else.
- **Corpus diff:** build the binary at `main` and at the branch; run both over the real-world Starlight corpora (`~/work/lambo/site`, `~/work/mooshik/docs`) with identical flags; every report (Markdown/HTML/JSON) must be byte-identical.
- Update `CLAUDE.md` (the `internal/config` and `internal/parser` architecture bullets describe the branch sites), `README.md` only if it documents behavior (it should not need to), and `CHANGELOG.md` under Unreleased as an internal refactor with no user-visible change.
