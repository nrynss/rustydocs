# Docusaurus profile

rustydocs analyzes `.md` and `.mdx` files with ATX (`#`) headings. It detects
Docusaurus from a regular `docusaurus.config.js` or `docusaurus.config.ts` file
at or above the content directory, within the repository boundary. The nearest
marker wins; existing profiles win same-directory ties. You can override
detection with `--profile docusaurus`.

```sh
rustydocs --content-dir ./docs --profile docusaurus
# For a checkout without the config marker:
rustydocs --content-dir ./docs --profile docusaurus --project-root .
```

## Markdown partials

The profile reuses the shared MDX import map. A static import introduces a
component name, and rendering that component adds the imported Markdown file's
Git date to the consuming section's freshness:

```mdx
import Foo from './_foo.mdx';
import { Note as LocalNote } from '../shared/_notes.md';

Welcome before the first heading.
<Foo />

# Setup

<Foo />
<LocalNote />

# Reference

This section does not render either partial, so it keeps its own Git age.
```

Import declarations do not manufacture a section or contribute their dates to
rendered prose. Content before the first heading remains analyzed and can
consume partials. Fenced headings, imports and component examples do not create
sections or dependencies. An unused import does not affect freshness.

Supported paths are `./…` and `../…` relative to the importing page, plus `/…`
relative to the detected or explicit project root. Imports follow `.md` and
`.mdx` files; extensionless paths try `.mdx`, `.md` and directory `index` files.
Relative paths do not fall back to Mintlify's `snippets/` directories. Exact
path case is required, and paths or symlinks escaping the project root cannot
contribute freshness. Reports retain the partial's project-relative identity
and dependency evidence at each rendered usage. Missing or uncommitted partials
are reported as unknown dependencies; they cannot supply a fresh date.

The [Docusaurus documentation](https://docusaurus.io/docs/markdown-features/react#importing-markdown)
describes the `_` prefix convention for partials. rustydocs does not require
that prefix or infer routing: underscore-prefixed Markdown files under
`content_dir` are also analyzed as files. To exclude their standalone rows,
use `exclude_patterns` (for example, `["**/_*.mdx", "**/_*.md"]`); referenced
partials still contribute their history. Content extensions and other explicit
configuration continue to override profile defaults.

## Supported subset and limits

This is static age-based triage, not a Docusaurus build or JavaScript evaluator.
The existing import scanner handles default and named imports, including local
symbol renames and multiline declarations, with uppercase component usages.
It attributes the imported file's date, rather than evaluating named exports
or React rendering.

- Aliases are skipped, including Docusaurus's official `@site/` alias (which
  Docusaurus resolves to the site directory), `@theme/`, `@theme-original/` and
  custom webpack/TypeScript aliases. An aliased Markdown import does not
  contribute freshness. Use a relative import for tracking.
- Package imports, loader-prefixed imports and code/assets such as `.js`,
  `.jsx`, `.ts`, `.tsx` and `.css` are skipped. Changes to a shared UI component
  cannot refresh every page that renders it.
- Dynamic imports, re-exports and global MDX component mappings are not traced.
  JSX member expressions and exports are not evaluated; imported namespace
  references are attributed to the whole Markdown file. Unimported components
  are out of scope.
- Config files are detected by name and kind only. Custom config filenames,
  config execution, CommonMark/MDX format switches, generated content,
  version/locale mapping and routing rules are not interpreted. Point
  `content_dir` at the content you want scanned; use explicit profile/root
  settings when markers are absent.

Skipped imports are not reported as broken dependencies. A missing relative
Markdown import that is actually rendered is reported unknown, as is a content
file with no resolvable Git history. Full repository history is required.
