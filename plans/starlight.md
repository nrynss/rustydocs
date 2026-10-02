# Astro Starlight profile implementation plan

## Scope

- Add a built-in `starlight` profile on `feat/starlight-profile-18`, selectable explicitly or detected from an `astro.config.{mjs,js,ts,mts}` that calls the `starlight()` integration, or a `package.json` depending on `@astrojs/starlight` (both validated by content, per the upstream manual setup).
- Analyze `.md`, `.mdx` and `.mdoc` (Markdoc, experimental upstream) files using ATX headings. Resolve MDX imports (`import X from "./_shared.mdx"`, used as `<X />`) through the shared import map and path resolver, attribute resolved history to the referencing section, and report broken content imports through existing diagnostics.
- Never report framework chrome: `.astro`/`.js` imports, bare package specifiers (`@astrojs/starlight/components`) and unimported components — Starlight's built-ins and Markdoc's import-free tags alike — are skipped by the import map's existing rules, so no component names are hardcoded.
- Keep existing profiles unchanged and document selection and limitations.

## Approach

- Parser needs no changes: `importmap.go` already skips non-content imports and bare specifiers and classifies unimported component-shaped captures as skipped; the profile only configures extensions, markers, predicates, patterns (`MDXComponentPattern` alone, per the provenance rule) and `ResolverPath` + `ImportMap: true`.
- Marker predicates `isStarlightConfig` (a `starlight(` call, not a bare import) and `isStarlightPackage` (the quoted `@astrojs/starlight` dependency) with a 1 MiB bounded read. `src/content.config.ts` is deliberately not a marker: plain Astro content collections have one too.
- `.mdoc` is analyzed as content but is deliberately not added to the import allowlist: Markdoc partials do not use ES imports, and skipping a non-`.md`/`.mdx` import is the safe failure direction.
- Add config tests (registry shape, selection per marker, predicate negatives) and parser tests pinned against the profile's own settings (classification table, Markdoc tags, section folding). Add a git-backed analyzer integration test with a Starlight tree, freshness folding, unresolved import and an outside-root escape.
- Update `README.md`, `CHANGELOG.md`, and `CLAUDE.md` per project convention.

## Validation

- Run targeted tests for `internal/config`, `internal/parser`, and `internal/analyzer`, then `go test ./...` including the profile-listing assertions, plus `go vet` and `gofmt -l .`.
- Verify with a temporary git-backed Starlight fixture that an imported partial's history affects its referring section and that built-in components, component imports and outside-root targets never resolve or report.
