# Astro Starlight profile implementation plan

## Scope

- Add a built-in `starlight` profile on `feat/starlight-profile-18`, selectable explicitly or detected from an `astro.config.{mjs,js,ts,mts}` that calls the `starlight()` integration, or a `package.json` depending on `@astrojs/starlight` (both validated by content, per the upstream manual setup).
- Analyze `.md`, `.mdx` and `.mdoc` (Markdoc, experimental upstream) files using ATX headings. Resolve MDX imports (`import X from "./_shared.mdx"`, used as `<X />`) through the shared import map and path resolver, and Markdoc partials (`{% partial file="./_footer.mdoc" /%}`, upstream-documented) as path-shaped include captures; attribute resolved history to the referencing section, and report broken content imports and partials through existing diagnostics.
- Never report framework chrome: `.astro`/`.js` imports, bare package specifiers (`@astrojs/starlight/components`) and unimported components — Starlight's built-ins and Markdoc's import-free tags alike — are skipped by the import map's existing rules, so no component names are hardcoded.
- Keep existing profiles unchanged and document selection and limitations.

## Approach

- The parser's only change is fence masking for starlight's reusable detection: `importmap.go` already skips non-content imports and bare specifiers and classifies unimported component-shaped captures as skipped, so the profile configures extensions, markers, predicates, patterns (two Markdoc partial include forms + `MDXComponentPattern`, per the provenance rule) and `ResolverPath` + `ImportMap: true`. Fenced content does not render, so examples shown in fences are not captured and cannot produce unresolved rows or fold dates.
- Marker predicates `isStarlightConfig` (a `starlight(` call, not a bare import) and `isStarlightPackage` (the quoted `@astrojs/starlight` dependency) with a 1 MiB bounded read. `src/content.config.ts` is deliberately not a marker: plain Astro content collections have one too.
- `.mdoc` is analyzed as content and its `{% partial file="…" /%}` includes are captured as paths (include patterns, so broken ones are unresolved); an explicit ES import of an `.mdoc` file stays off the import allowlist and is skipped — the safe failure direction, since Markdoc reuses content through the partial tag, not ES imports. The partial patterns are additionally gated to `.mdoc` (#75): Markdoc tags render there only, so an unfenced tag on a `.md`/`.mdx` page — inert syntax — is neither captured nor resolved.
- Add config tests (registry shape, selection per marker, predicate negatives) and parser tests pinned against the profile's own settings (classification table, Markdoc tags, section folding). Add a git-backed analyzer integration test with a Starlight tree, freshness folding, unresolved import and an outside-root escape.
- Update `README.md`, `CHANGELOG.md`, and `CLAUDE.md` per project convention.

## Accepted trade-offs

- Markdoc partials written with an **import alias** (`{% partial
  file="@partials/footer.mdoc" /%}`, or the key of `markdoc.config`'s
  `partials` map) are captured — the quoted patterns match any file attribute
  — but when the capture resolves to no literal file under the project root
  it is classified as deliberately out of scope, not a broken include (#74).
  Upstream resolves such attributes through Vite module resolution
  (`resolvePartials` calls `pluginContext.resolve(file, …)` with a
  `'./' + file` page-relative fallback), so the name may map through alias
  configuration this tool does not read; a genuinely broken alias fails the
  Astro build regardless. A bare name that does exist page-relative still
  resolves (the fallback upstream performs). The `file={import('…')}`
  expression form the issue hypothesised does not exist upstream — no
  example in the docs, none in the wild, and Markdoc expressions cannot call
  `import()` — and is deliberately not captured: folding a date for syntax
  Astro never renders would over-report freshness, the failure #75 exists to
  prevent.
- Imports through tsconfig path aliases (`import X from "@/components/x.mdx"`)
  are classified as bare package specifiers and silently skipped: the import
  map resolves only `/`, `./` and `../` specifiers, and reading tsconfig
  `paths` is out of scope. That is the safe direction — no false freshness, no
  false broken include — but an alias-imported partial's history does not fold
  into the referencing section. Relative imports, which the observed corpora
  use exclusively, are unaffected.
- A content import that escapes the project root (a monorepo Starlight site
  reaching `../../../packages/shared/x.mdx`) counts as an unresolved include:
  `withinRoot` rejects the candidate and the import-map rule reports any
  content import that resolves to nothing. That is the conservative direction —
  a broken reference is visible rather than silently folded — and no observed
  corpus does it; revisit if a real monorepo complains.
- Component captures are not fence-masked for mintlify and hugo
  (`FindReusables` computes `fencedSpans` for the gitbook and starlight
  profiles only), so on those profiles a page that *documents* a component it
  genuinely imports elsewhere folds that partial's date into the documenting
  section too. Pre-existing mintlify behaviour, unchanged there; under
  starlight fenced content is not captured at all, so fenced partial examples
  produce no rows.

## Validation

- Run targeted tests for `internal/config`, `internal/parser`, and `internal/analyzer`, then `go test ./...` including the profile-listing assertions, plus `go vet` and `gofmt -l .`.
- Verify with a temporary git-backed Starlight fixture that an imported partial's history affects its referring section and that built-in components, component imports and outside-root targets never resolve or report.
