# GitBook profile implementation plan

## Scope

- Add a built-in `gitbook` profile on `feat/gitbook-profile-20`, selectable explicitly or detected from `.gitbook.yaml` or `SUMMARY.md`.
- Analyze `.md` files using ATX headings. Capture `{% content-ref url="..." %}` and `{% include "..." %}` references, attribute resolved Markdown history to the referencing section, and report unresolved references through existing diagnostics.
- Keep existing profiles unchanged and document selection and limitations.

## Approach

- Retain and review the existing uncommitted GitBook registry and shape tests in `internal/config/profile.go` and `internal/config/profile_test.go`.
- Use the existing path resolver and parser pipeline. GitBook bare paths should prefer the referring page's directory, then the project root; unlike Mintlify they should not prefer `snippets/`. Make this ordering profile-specific while preserving Mintlify's current lookup order and root boundary checks.
- Add config and parser regressions for selection, captures, relative and bare paths, unresolved references, and project boundaries. Add a git-backed analyzer integration test for history attribution and unresolved targets.
- Update `README.md` and release notes if project convention warrants it.

## Validation

- Run targeted tests for `internal/config`, `internal/parser`, and `internal/analyzer`, then `go test ./...` including the profile-listing assertions.
- Verify with a temporary git-backed GitBook fixture that referenced file history affects its referring section and missing or outside-root targets do not resolve.