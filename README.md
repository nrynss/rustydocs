# rustydocs

[![Go Reference](https://pkg.go.dev/badge/github.com/nrynss/rustydocs.svg)](https://pkg.go.dev/github.com/nrynss/rustydocs)

Find stale documentation using git history. Analyzes your documentation at the section level to identify content that hasn't been updated recently.

## Features

- **Section-level analysis**: Uses `git blame` to analyze staleness per section, not just per file
- **Works on any Markdown repo out of the box**: the default `markdown` profile analyzes `.md`/`.markdown` files with no setup
- **Tool profiles**: `hugo`, `mintlify`, `gitbook`, `starlight`, and `docusaurus` profiles add tool-specific include tracking; see [Profiles](#profiles)
- **Component tracking**: Under the `hugo` profile, detects Hugo shortcodes (`{{< >}}`, `{{% %}}`) and JSX/MDX components (`<Component>`); under `mintlify`, both `<Snippet file="foo.mdx" />` includes and MDX imports (`import X from "/snippets/x.mdx"` rendered as `<X />`); under `starlight`, MDX imports (`import X from "./_shared.mdx"` rendered as `<X />`) and Markdoc partials (`{% partial file="./_footer.mdoc" /%}`); under `docusaurus`, static relative Markdown imports rendered as components — and folds their freshness into the section that uses them. Component imports (`.jsx`/`.js`/`.css`, and `.astro` under `starlight`) are deliberately skipped, since a restyle must not make every page that uses them look fresh
- **Scans documentation, not tooling**: dot-directories, vendored and build trees, nested standalone repositories (but not submodules) and git-ignored files are excluded by default (`--no-default-excludes` to opt out) — on a real 4,000-file docs repo that is about a 6.5x speedup (48.7 s to 7.5 s) and 1,078 fewer spurious *unknown* rows
- **Parallel processing**: Analyzes multiple files concurrently using goroutines
- **Three output formats**: Generates JSON, Markdown and self-contained HTML reports
- **Zero dependencies**: Uses only Go standard library
- **Configurable thresholds**: Set custom staleness levels (warning, caution, critical)

## Installation

```bash
# Build from source (includes version info)
make build
./rustydocs --version

# Or install directly (binary lands in $(go env GOPATH)/bin, or $GOBIN if set)
go install github.com/nrynss/rustydocs/cmd/rustydocs@latest
rustydocs --version
```

Binaries installed with `go install ...@latest` report the module version (for
example `rustydocs vX.Y.Z` instead of `rustydocs dev`) from the Go build info
embedded by the toolchain. Builds made from a local clone without ldflags
(`go build ./cmd/rustydocs`) additionally show the commit from the embedded VCS
info. The build date is only reported when set via ldflags (`make build`);
release binaries keep all the values set via ldflags.

### Release archives

Tagged releases produced by GoReleaser provide Linux, macOS (`darwin`), and
Windows archives for both `amd64` and `arm64` on the
[releases page](https://github.com/nrynss/rustydocs/releases).
Archives are named `rustydocs_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows)
and contain the `rustydocs` binary (`rustydocs.exe` on Windows), license,
README, and changelog. Extract the archive and put the binary on your `PATH`.
Git must also be installed and available on `PATH`.

Download `checksums.txt` alongside your archive to verify its SHA256 checksum:

```bash
# Linux (checks downloaded archives only)
sha256sum --check --ignore-missing checksums.txt
# macOS
shasum -a 256 --check --ignore-missing checksums.txt
```

To validate release packaging locally, install
[GoReleaser v2.18.2](https://goreleaser.com/install/) and Docker with Buildx
and amd64/arm64 support, then run:

```bash
goreleaser check
make release  # builds archives in dist/ and local container images; never publishes
```

CI builds snapshots when pull requests or pushes to `main` change code, build
inputs, embedded assets, or test fixtures. Documentation-only changes skip the
expensive CI jobs. Pushing a
`v*` tag runs `goreleaser release --clean` and publishes the archives and
`checksums.txt` with GitHub-generated release notes. Release binaries retain
the tag version, full commit SHA, and UTC build date; snapshot versions are
marked as snapshots. Prerelease tags are published as GitHub prereleases.

### Container image

The release workflow publishes `ghcr.io/nrynss/rustydocs` for Linux `amd64` and
`arm64`. The image contains a prebuilt rustydocs binary, Git, and certificates
on Alpine; no Go installation is required. Stable releases get their exact
`vX.Y.Z` tag and `latest`; prereleases get only their exact tag. Prefer an exact
version or digest in CI for reproducibility.

Run from the **repository root**, mounting the entire checkout including `.git`:

```bash
mkdir -p reports
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD:/src:ro" \
  -v "$PWD/reports:/reports" \
  ghcr.io/nrynss/rustydocs:latest \
  --content-dir /src/docs --output-dir /reports
```

Use your host UID/GID so reports remain writable and owned by you. The default
image user is `65532:65532`; when using that default, grant it read access to the
checkout and write access to the output directory. The image trusts `/src` as a
Git repository even when its owner differs from the container user. For a
checkout mounted elsewhere, match its owner or supply a specific Git
`safe.directory` through Git's environment configuration. Mount linked-worktree
Git directories and any submodule Git storage at their referenced paths too.

The checkout needs **full history**: use `fetch-depth: 0` with
`actions/checkout`, or run `git fetch --unshallow` for a shallow clone before
scanning. Shallow history is reported in diagnostics and can give misleading
ages. The container analyzes the existing checkout; it does not clone or fetch.

Docker Desktop/Colima must have file-sharing access to the mounted directories.

Local snapshot builds also create images tagged
`ghcr.io/nrynss/rustydocs:<snapshot-version>-amd64` and `-arm64`. CI tests both
against mounted repositories, including file ownership, missing history,
shallow-history diagnostics, and writable reports. Run the same smoke test with:

```bash
python3 scripts/test-container.py <snapshot-image-tag> linux/amd64
```

Smoke fixtures are created under the current directory; set
`RUSTYDOCS_TEST_TMPDIR` to another Docker-shared directory if needed.

The Dockerfile consumes GoReleaser's temporary binary context; use `make release`
to build images instead of running `docker build .` against the source tree.
The release workflow requires successful push-to-main CI on the exact tagged
commit (waiting if that run is still in progress), then packages and publishes
without repeating tests, lint, or container scans. Tag a commit whose CI has
passed.

On first GHCR publication, verify that the package visibility is **public** so
unauthenticated CI users can pull it.

## Quick Start

```bash
# Run with default settings (90 day threshold)
rustydocs --content-dir ./docs --output-dir ./reports

# Use a config file
rustydocs --config config.json

# Custom threshold
rustydocs --content-dir ./docs --threshold-days 180
```

## Profiles

A **profile** describes how a documentation tool lays out its content: which file
extensions are documentation, how the project root is located, and how (if at
all) reusable/included content is referenced and resolved. Profiles only supply
defaults; anything you set explicitly (`--extensions`, `content_extensions`,
`reusables.patterns`, `reusables.dir`, `--project-root`/`project_root`) always
wins.

| Profile    | Extensions                 | Root marker         | Reusable detection                                   |
| ---------- | -------------------------- | ------------------- | ---------------------------------------------------- |
| `markdown` | `.md`, `.markdown`         | none                | **off** (plain CommonMark/GFM has no include mechanism) |
| `gitbook`  | `.md`                      | `.gitbook.yaml` or `SUMMARY.md` | `{% content-ref url="…" %}` and `{% include "…" %}` paths |
| `docusaurus` | `.md`, `.mdx`            | `docusaurus.config.js` or `docusaurus.config.ts` file | Static Markdown partial imports (`import Foo from './_foo.mdx'` rendered as `<Foo />`), using relative or project-root-relative paths; fenced examples, code/package imports and aliases (including `@site` and `@theme`) are skipped. See [supported subset](docs/docusaurus.md). |
| `mintlify` | `.md`, `.mdx`              | `docs.json` (current) or `mint.json` (legacy) file whose contents look like a Mintlify config | `<Snippet file="…" />` (either quote style) and MDX imports rendered as `<X />`, resolved as a **path** under `snippets/` / `_snippets/` or the project root; `.jsx`/`.js`/`.css` imports are skipped |
| `hugo`     | `.md`, `.markdown`, `.mdx` | `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config | Hugo shortcodes + MDX/JSX components, resolved via `layouts/shortcodes` and `themes/*/layouts/shortcodes` |
| `starlight` | `.md`, `.mdx`, `.mdoc`    | an `astro.config.{mjs,js,ts,mts}` whose contents register the `starlight()` integration (whole-line comments are ignored), or a `package.json` depending on `@astrojs/starlight` | MDX imports (`import X from "./_shared.mdx"`) rendered as `<X />` and Markdoc partials (`{% partial file="./_footer.mdoc" /%}`, captured on `.mdoc` pages — where Markdoc tags render — only), resolved as a **path** within the project root; fenced examples are not captured; `.astro`/`.js` imports, bare package specifiers and tsconfig path aliases (`@/…`), and unimported components — Starlight's built-ins and Markdoc's import-free tags alike — are skipped |

**Auto-detection.** When no profile is named, rustydocs walks up from
`content_dir` one directory at a time looking for the profiles' root markers;
the marker nearest to `content_dir` wins. `.gitbook.yaml` or `SUMMARY.md`
selects `gitbook`; a `docs.json` or `mint.json` file
selects `mintlify`; a `layouts/` or `themes/` directory, a
`hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config — `hugo.*`
or `config.*` under that directory — selects `hugo` (the config-file and
`themes/` markers matter for fresh clones of theme-based sites, since git does
not track an empty `layouts/` directory); and an `astro.config.{mjs,js,ts,mts}`
that registers the `starlight()` integration — or a `package.json` depending
on `@astrojs/starlight` — selects `starlight`. A `docusaurus.config.js` or
`docusaurus.config.ts` file selects `docusaurus` by name and file kind; its
JavaScript/TypeScript contents are not executed. A nested docs tree therefore
wins over a marker further up: a Mintlify `docs.json` inside a repo that also
has a `layouts/` at its root selects `mintlify`.

The Mintlify and Starlight markers are checked by **content** as well as by
name: a `docs.json` or `mint.json` selects the profile only when it parses as
a JSON object carrying a recognisably Mintlify key (`navigation`, `theme`,
`colors`, `logo`, `favicon`, `tabs`, `anchors`, or a `$schema` mentioning
Mintlify), and an `astro.config.*` selects `starlight` only when it actually
*calls* the `starlight()` integration (importing the package without
registering it does not count), while a `package.json` counts only when it
depends on `@astrojs/starlight` — the file alone is too generic to be a
marker. A file that merely has the name — some other tool's `docs.json`, or a
malformed one — is ignored, and detection carries on up the tree. Only when
markers of more than one profile sit in the *same* directory does registry
order decide, and the earlier-registered profile wins — which is why
**`hugo` beats `mintlify`** there (`layouts/` and `hugo.toml` are unambiguous
evidence, and picking `mintlify` would silently switch shortcode tracing off
on a Hugo site that happens to ship a `docs.json`). The same-directory order is
`gitbook`, `hugo`, `mintlify`, `starlight`, then `docusaurus`; Docusaurus is
appended to preserve existing precedence. Pass `--profile` to override.

If nothing is found, the `markdown` profile is used. **A project root you
supply never selects a profile**: `--project-root` / `project_root` says where
reusable references resolve *from*, not what the project *is*, so a Hugo or
Mintlify project matching none of these markers must pass `--profile` as well —
for Mintlify, together with `--project-root`, since the profile has no root to
resolve snippet paths against otherwise (rustydocs says so on stderr when
that happens). A project root you supply yourself must exist and be a
directory: a mistyped path fails the run immediately rather than producing a
report in which nothing resolves.

The deprecated `hugo_root` key is the one exception, for backward
compatibility: a config that sets it and names no profile still gets `hugo`
when no marker is found anywhere, which is what that key used to mean. Rename
it to `project_root` — rustydocs prints a deprecation notice while you still
have the old spelling.

A marker file that rustydocs cannot *read* (permissions) is skipped, but it is
not swallowed either — the run prints a warning naming the file and the profile
whose marker it is, so a `chmod 000 docs.json` does not silently look like a
plain Markdown project. The warning is about that one marker: if another marker
in the same directory still matches (a readable `mint.json` next to the
unreadable `docs.json`), the profile is selected as usual and the warning just
records what was skipped.

The walk never leaves the enclosing git repository: it stops at the directory
holding `.git` (a marker sitting next to `.git` still counts), so a checkout
that happens to live under some unrelated `themes/` or `layouts/` directory is
not mistaken for a Hugo site. A **submodule** is the one exception: when the
`.git` entry is a file pointing into the parent repository's `.git/modules/`,
the walk continues up into that parent, so a Hugo site whose `content/` is a
submodule is still detected from the `layouts/` and `hugo.toml` that live one
level above it. A `.git` file pointing at a linked worktree
(`.git/worktrees/…`), and any `.git` file that cannot be read or parsed, stops
the walk. Outside a repository the walk continues to the filesystem root. The
resolved profile is printed in the run banner, e.g.
`Profile: markdown (auto-detected)`.

```bash
rustydocs --list-profiles                          # print the built-in profiles
rustydocs --content-dir ./docs --profile hugo      # force a profile

# A Mintlify project: docs.json at the repo root is detected automatically, and
# every <Snippet file="…" /> on a page folds the snippet's own commit
# date into the section that includes it.
rustydocs --content-dir ./docs --output-dir ./reports --threshold-days 90
rustydocs --content-dir ./docs --profile mintlify  # or name it explicitly

# No docs.json (a docs subtree checked out on its own, say)? Name the root, or
# snippet paths have nothing to resolve against.
rustydocs --content-dir ./docs --profile mintlify --project-root .

# GitBook: .gitbook.yaml or a GitBook-style SUMMARY.md selects it automatically.
rustydocs --content-dir ./docs --profile gitbook

# Astro Starlight: an astro.config.* calling starlight(), or a package.json
# depending on @astrojs/starlight, selects it automatically. Point --content-dir
# at the content collection (src/content/docs) or the repo root; imports such as
# `import X from "./_shared.mdx"` fold the partial's history into the section
# that renders <X />, while Starlight's built-in components stay out of the
# report.
rustydocs --content-dir ./src/content/docs --profile starlight

# Docusaurus: docusaurus.config.js/.ts selects it automatically. Static relative
# Markdown imports contribute freshness only in sections rendering the component.
rustydocs --content-dir ./docs --profile docusaurus
# Without a marker, specify both the profile and the site root.
rustydocs --content-dir ./docs --profile docusaurus --project-root .

# --project-root alone never changes the profile: on a repo with a docs.json
# this is still a mintlify run.
rustydocs --content-dir ./docs --project-root .
```

Or in `config.json`: `"profile": "hugo"` (empty string = auto-detect).

GitBook references resolve relative to the containing page, including bare
include paths; paths beginning with `/` use the project root. There is no
implicit project-root fallback for relative includes. Directory references
try `README.md` before `index.md` as a compatibility heuristic, not a
documented GitBook rule. Both single- and double-quoted directives are
supported as a compatibility heuristic, including `content-ref` with attributes
before `url`. Only in-root Markdown files with Git history contribute to section
freshness. Missing, uncommitted, or outside-root references are reported unknown;
other GitBook directives and remote URLs are not resolved. Set `--project-root`
when selecting `gitbook` explicitly without a marker; GitBook's `SUMMARY.md`
is a marker, not a navigation filter. Auto-detection from `SUMMARY.md` requires
a `# Summary` heading and at least one Markdown navigation link; other summary
files fall back to `markdown`. Directives in fenced code examples are ignored.

Backward compatibility: pointing at a reusables directory (`reusables.dir`,
`reusables_dir`, or `--reusables-dir`) without setting `reusables.patterns`
restores the Hugo defaults that flow was built for even under the `markdown`
profile — both the shortcode / JSX pattern list and the Hugo content
extensions, so `.mdx` files keep being analyzed alongside `.md`/`.markdown`.
Explicit `--extensions` / `content_extensions` and `reusables.patterns` still
win.

## Configuration

Create a `config.json` file:

```json
{
  "threshold_days": 90,
  "profile": "",
  "content_dir": "src/hugo/docsy/content/en",
  "project_root": "src/hugo/docsy",
  "output_dir": "./reports",
  "exclude_dirs": ["images", "releasenotes"],
  "staleness_levels": {
    "warning": 90,
    "caution": 180,
    "critical": 365
  }
}
```

### Configuration Options

| Option                 | Description                                        | Default                      |
| ---------------------- | -------------------------------------------------- | ---------------------------- |
| `threshold_days`       | Days before content is considered stale            | 90                           |
| `git_last_modified`    | Batch file-level Git dates when supported, otherwise use cached `git log`. CLI: `--git-last-modified=false` to disable | true |
| `profile`              | Documentation profile (`markdown`, `gitbook`, `mintlify`, `hugo`, `starlight`, `docusaurus`); empty = auto-detect | (auto-detect)   |
| `content_dir`          | Directory containing documentation files           | (required)                   |
| `content_extensions`   | File extensions to analyze                         | from profile                 |
| `project_root`         | Project root reusables resolve against — the Hugo site root, the Mintlify snippet root, or the GitBook root bounding page-relative includes and content references (auto-detected for any profile with root markers). Never influences which profile is selected. CLI: `--project-root` | (auto-detect)   |
| `hugo_root`            | **Deprecated** spelling of `project_root`. On its own it still supplies the root, with a deprecation warning telling you to rename it; when `project_root` (or `--project-root`) is set too, that one wins and `hugo_root` is *ignored*, with a warning saying so. Unlike `project_root` it keeps its legacy side effect: it selects the `hugo` profile when no marker is found | (auto-detect)          |
| `output_dir`           | Output directory for reports                       | `./reports`                  |
| `reusables.dir`        | Directory containing reusable component files      | (optional)                   |
| `reusables.patterns`   | Regex patterns to detect reusables (capture group) | from profile (`hugo`: shortcodes + JSX; `gitbook`: content-ref + include; `mintlify`: `<Snippet file>` + imported components; `markdown`: none) |
| `exclude_patterns`     | Glob patterns to exclude files (additive on top of the default exclusions) | `[]`                         |
| `exclude_dirs`         | Directory names to exclude entirely — the whole subtree is pruned from the walk, like the default exclusions (additive on top of them) | `[]`                         |
| `no_default_excludes`  | Turn off the default exclusions (dot-directories, `node_modules`/`vendor`/`dist`/`build`, nested standalone repositories, and git-ignored files) and scan everything. CLI: `--no-default-excludes` | false |
| `staleness_levels`     | Thresholds for warning/caution/critical            | 90/180/365                   |
| `paragraph_level`      | Analyze at paragraph level (more granular)         | false                        |

### Default Exclusions

The content walk prunes three kinds of directory and one kind of file before
anything is blamed, because a real docs repo is mostly not documentation:

1. **Dot-directories** (`.git/`, `.claude/`, `.cursor/`, `.github/`, …) and the
   usual vendored and build trees (`node_modules/`, `vendor/`, `dist/`,
   `build/`).
2. **Any nested standalone repository** — a clone or a linked worktree checked
   out inside the tree. Those files belong to a *different* project, so their
   dates describe someone else's history. A **submodule** is deliberately not
   one of these: it is part of the repository under analysis, docs sites use it
   to share a content tree, and it is walked into and analyzed like any other
   directory (its files resolve against the submodule's own repository, which
   is where their history lives).
3. **Files git ignores.** rustydocs asks git itself, in one batched
   `git check-ignore` per repository the walk touched, so nested `.gitignore`
   files, negations, `core.excludesFile` and the index are all honoured
   exactly. A *tracked* file is never dropped, however the patterns read.

A content tree that is not a git repository still works: the name rules apply,
the ignore query is skipped, and its files are reported *unknown* as they always
were. The content root itself is never pruned, so `--content-dir .` at a
repository root, or pointing straight at a dot-directory, does what you meant.

Measured on a production Mintlify site, this took a run from 4,032 files in
48.7 s with 1,078 files reported as having no git history, to 552 files in 7.5 s
with none.

When the defaults remove anything, rustydocs prints a note on stderr naming the
count, the directories and the flag that puts them back. Pass
`--no-default-excludes` (or set `"no_default_excludes": true`) to scan
everything; `--exclude-dirs` and `exclude_patterns` are unaffected by it and
always apply on top.

### Hugo Shortcode Tracing

Under the `hugo` profile, when you use Hugo shortcodes like `{{< alert >}}`, rustydocs automatically:

1. Finds the shortcode template in `layouts/shortcodes/alert.html`, falling
   back to each theme's `themes/<theme>/layouts/shortcodes/alert.html` (a
   project template of the same name wins, matching Hugo's own lookup order)
2. Parses the template for data references (`readFile`, `partial`, `.Site.Data`)
3. Tracks freshness of both the template and any data files it uses

This means if your content uses `{{< reusables/warning >}}` and that shortcode reads from `data/reusables/warning.md`, the staleness check includes the data file's last modification date.

The project root is auto-detected by walking up from `content_dir` (no further than the enclosing git repository root, though a submodule `content/` is walked through into its parent repository) until finding a `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config; finding one is also what selects the `hugo` profile. Set `project_root` / `--project-root` to override the detected root; on a site with none of those markers, pair it with `--profile hugo`.

Templates, themes and their supporting files must stay within the selected
project root after resolving symlinks. In-root symlinks and explicitly selected
symlinked roots work; a discovered theme symlink to an external checkout is
rejected. The legacy `reusables.dir` permits legacy file lookup within that
directory only and does not grant Hugo templates permission to read it. Rejected
files contribute no content fingerprints or Git dates. Missing or rejected
support produces contextual diagnostics and partial dependency evidence in all
reports, even when permitted support makes a section fresh.

### Mintlify Snippet Resolution

Under the `mintlify` profile, a snippet include carries the file path outright,
so there is nothing to trace — the path *is* the answer:

```mdx
<Snippet file="aws-access-key-config.mdx" />
<Snippet file="cloud/prerequisites.mdx" />
```

Mintlify resolves a `file=` like that against the project's **snippets
directory**, not against the page and not against the project root, and that
bare form is what real projects overwhelmingly write. The **path resolver**
follows the same order:

1. A path starting with `/` is resolved against the **project root** (the
   directory holding `docs.json` / `mint.json`) and nowhere else — Mintlify's
   root-absolute form.
2. A path starting with `./` or `../` is resolved against the directory of the
   **page that references it** first, then falls through to the bases below.
3. Anything else — a bare filename or a bare relative path, the common form —
   is resolved against `<root>/snippets/`, then `<root>/_snippets/`, then the
   project root, and only then against the referencing page's directory as a
   tolerant last resort. A name present in both `snippets/` and next to the
   page therefore resolves to `snippets/`, which is what Mintlify renders.
4. A path with no extension is tried against `.mdx` and `.md` (and then against
   `<path>/index.mdx`, `<path>/index.md`), so a slightly loose reference still
   resolves. The first candidate that exists wins.
5. A path that leaves the project root is **ignored**, so a report never reaches
   outside the docs project. Containment is checked after resolving symlinks on
   both the candidate and the root, so neither a `..` traversal nor a symlink
   inside the tree that points somewhere else (`snippets/out -> /elsewhere`)
   can fold a foreign file's commit date into the report. A symlink whose
   target stays inside the root resolves normally.

The resolved file's last commit date is folded into the section that includes
it, exactly as Hugo shortcodes are — a stale-looking page whose snippet was
updated last week is not stale. A reference that resolves to nothing (a missing
or uncommitted snippet) is reported as *unknown*, never as fresh.

Resolution needs a project root. Under auto-detection that is the directory
holding `docs.json` / `mint.json`; on a tree with neither (or with
`--profile mintlify` forced), pass `--project-root PATH` — otherwise there is
nothing to resolve against and every snippet is reported *unknown*.

Whenever any reference comes back unresolved, rustydocs prints a note on stderr
counting them and naming the profile, whether or not a root was found; when the
root is what is missing, the note says so and points at `--project-root`. A run
where everything resolved, or one with no includes at all, stays quiet. The
exit code is unchanged either way.

Reports name a snippet by its path relative to the project root, and that path
comes from resolving the reference, not from git — so two pages that both write
`<Snippet file="new.mdx" />` and mean two different files stay two rows even
when neither file has been committed yet.

Both quote styles are recognised (`file="…"` and `file='…'`), in any attribute
position. Only `<Snippet …>` itself counts as a snippet include: a
`<SnippetGroup file="…">` is a different element and is **not** one, because its
`file=` does not identify a snippet the same way.

#### MDX imports

Most Mintlify projects do not write `<Snippet file="…" />` at all — they import
the snippet and render it as a component:

```mdx
import Prerequisites from "/snippets/prerequisites.mdx";
import { StepOne, StepTwo } from "./setup-steps.mdx";
import { YamlTable } from "/snippets/yaml-table.jsx";

# Getting started

<Prerequisites />
<StepOne />
<YamlTable rows={rows} />
<Card title="not an include" />
```

rustydocs reads the imports at the top of each page — default, named, aliased
(`A as B`), namespace and combined forms, written over one line or several —
and builds a symbol-to-path map for the whole file before attributing anything,
because imports sit above the sections that use them. A section's `<X />` then
folds the imported file's commit date into that section's freshness, exactly as
a `<Snippet file="…" />` does. Import paths resolve the same way as snippet
paths: a leading `/` against the project root, `./` and `../` against the
importing page, with the same containment and symlink checks. Both forms work on
the same page.

**Only content imports are followed** — `.md` and `.mdx`, plus an extensionless
path such as `./intro`, which is what a content import looks like in an MDX tree
and which the resolver's extension fallback tries `.mdx`/`.md` (and `index.*`)
against. A `.jsx`, `.js` or `.css` import, or a package import
(`@mintlify/components`), is detected and deliberately **skipped**. This is not an oversight — folding an include's date
in only ever makes a section look *fresher*, so resolving a shared React
component would mark every page importing it as recently updated the next time
someone changed its styling, destroying the signal precisely where the tool is
supposed to provide it. Under-reporting freshness is the safe direction.

A skipped import is **not** reported as an unresolved reusable, and neither is a
capitalised tag that no import introduced (`<Card />`, `<Tabs>`, `<Accordion>`):
those are layout components, not includes, and are out of scope by design rather
than broken. An import of an `.mdx` file that does not exist, or that exists but
has never been committed, *is* still reported *unknown* and counted in the
stderr note.

### Default Component Patterns

The `hugo` profile detects (the `markdown` profile detects nothing unless you set `reusables.patterns`; the `mintlify` profile detects `<Snippet file="…" />` and imported components, see above):

**Hugo shortcodes** (all styles):
- `{{< shortcode >}}`
- `{{% shortcode %}}`
- `{{< shortcode param="value" >}}`

**MDX/JSX components**:
- `<Alert>`
- `<CodeBlock />`
- `<Tabs item="foo">`

## CLI Options

```
rustydocs [OPTIONS]

Options:
  --config PATH           Path to JSON config file
  --content-dir PATH      Directory containing documentation files
  --reusables-dir PATH    Directory containing reusable components
  --output-dir PATH       Output directory for reports
  --threshold-days INT    Days before content is considered stale (default: 90)
  --exclude-dirs STRING   Comma-separated directories to exclude (additive on top of
                          the default exclusions)
  --no-default-excludes   Scan everything: turn off the default exclusions
                          (dot-directories, build/dist/node_modules/vendor, directories
                          holding their own .git, and files git ignores).
                          --exclude-dirs / --exclude-patterns still apply.
                          Config-file spelling: "no_default_excludes"
  --extensions STRING     Comma-separated documentation extensions to analyze (default: from profile)
  --profile NAME          Documentation profile (see --list-profiles; default: auto-detect)
  --project-root PATH     Project root that reusable references resolve against (Hugo site
                          root, Mintlify docs root); default: detected from the profile's
                          markers. It never selects a profile on its own, so pair it with
                          --profile on a project whose markers are absent. Config-file
                          spelling: "project_root" (deprecated: "hugo_root")
  --list-profiles         List built-in profiles and exit
  --file-level-only       Skip section-level analysis (faster)
  --git-last-modified     Use experimental batch file dates (default: on;
                          falls back to git log). Config: "git_last_modified"
                          Disable with --git-last-modified=false
  --paragraph-level       Analyze at paragraph level (more granular)
  --workers INT           Number of parallel workers (default: number of CPUs)
  --version               Show version and exit
```

### Git accelerator (enabled by default)

rustydocs enables the experimental `git last-modified` accelerator by default
for file-level and reusable-file dates. It does not replace per-line blame or
change section-level analysis, authors, commit IDs or date semantics. Older Git
versions and unsupported or unusable accelerator results fall back to cached
per-file `git log` lookups.

To disable batching and its capability probes, use:

```bash
rustydocs --content-dir ./docs --git-last-modified=false
```

Alternatively, set `"git_last_modified": false` in your JSON config. Explicit
CLI values override config: `--git-last-modified` (or `=true`) re-enables it,
and `--git-last-modified=false` disables it even when config says true.

The fast path requires Git's recursive NUL-delimited `last-modified` output
(available in Git 2.54). Capability and output checks decide eligibility rather
than the version string. For correctness, merge histories, shallow repositories,
replacement objects/grafts, and history/pathspec overrides use the fallback.
Setting `GIT_CONFIG` also disables the fast path: it can redirect configuration
probes without redirecting history commands.
Paths absent from HEAD, missing paths, directories and special pathspec spellings
also use the existing per-file lookup. A failed batch is not retried for every
file during the same scan. Keep the checkout stable during analysis as usual.

Batching is intended for many distinct file/reusable lookups, not repeated hits
on one already-cached snippet, where initialization can be slower; use the
disable flag for those workloads. To compare both workloads on your machine:

```bash
go test ./internal/git -run '^$' -bench BenchmarkFileInfoCache -benchmem -benchtime=3x
```

Each benchmark iteration creates a fresh run-scoped cache against eight linear
commits, with repository-root discovery warmed for both modes. It measures Git
lookups only, not a complete documentation scan. On Apple M3 Pro / Git 2.54,
one three-iteration sample took 831 ms vs 159 ms for 32 distinct paths and
6.79 s vs 166 ms for 256 distinct paths (cached log vs batch). Repeated lookups
of one shared path were slower with batch initialization (33 ms vs 157 ms at
32 lookups). Results depend on repository history, filesystem and Git version.

## Output

Three reports are generated in the output directory:

| File | Format | Use Case |
|------|--------|----------|
| `stale-docs.json` | JSON | CI/CD pipelines, GitHub Actions, tooling |
| `stale-docs.md` | Markdown | Pull requests, git-friendly review |
| `stale-docs.html` | HTML | Visual dashboard, sharing |

### JSON Report (`stale-docs.json`)

Schema `2.0` exports every analyzed section, including fresh and unknown sections,
with portable file/repository IDs, content fingerprints, own and effective change
evidence, snippet dependencies, repository revision/dirty/shallow context, effective
configuration, diagnostics and coverage counters. Unknown dates and ages are null.
Files group sections and have no age or last-updated score.

See the [JSON v2 contract and migration guide](docs/json-v2.md) and
[complete example artifact](docs/examples/scan-v2.json). Consumers can filter
`files[].sections[].is_stale` for a stale-only view. Check `history_status`,
`diagnostics` and `coverage` before interpreting a clean run; shallow history and
untracked files can limit the evidence available. Keep the checkout stable during
analysis. One analysis timestamp drives classification and all exported ages.

Snippet provenance identifies which supporting file supplies a section's freshness.
It does not say that the snippet changed since a previous run or that surrounding
prose is correct. Historical comparisons, scheduling, storage and review decisions
belong to the consuming application.

### Markdown Report (`stale-docs.md`)

Markdown groups stale sections by file and lists section dates, ages, effective
authors, severity and snippet evidence. Separate sections show consuming sections
made fresh by snippets, unknown or partial history, scan diagnostics and the
reusable inventory. File groups have no page-age display.

### HTML Report (`stale-docs.html`)

- Self-contained, read-only report with a compact summary and navigation
- Expandable file groups with section severity and snippet evidence
- Fresh sections whose effective date comes from a newer snippet
- Visible unknown/partial history details and scan diagnostics
- Independent table sorting for dates, numeric ages/lines and text, with unknown
  values last in either direction

The report contains no persisted review state or review-status controls.

## How It Works

1. **Resolves** a profile (`--profile`, or auto-detected from the content dir) and **scans** the files whose extensions it lists
2. **Parses** content to identify sections by headers (`#`, `##`, `###`); content above the first header is analyzed as a leading `(preamble)` section, with `---`/`+++` frontmatter and non-rendered MDX imports skipped; fenced code cannot introduce headings
3. **Runs** `git blame` concurrently to get per-line modification dates
4. **Detects** reusable components (Hugo shortcodes, JSX — `hugo` profile; `<Snippet file>` includes — `mintlify` profile) and checks their freshness
5. **Calculates** section staleness from its most recent committed line, folded with resolved supporting snippet dates
6. **Generates** reports in JSON, Markdown, and HTML

## GitHub Actions

Use the JSON output in CI/CD pipelines:

```yaml
- name: Check documentation freshness
  run: |
    rustydocs --content-dir ./docs --output-dir ./reports

    # Fail if critical stale content exists
    if jq -e '.files[].sections[] | select(.is_stale and .severity == "critical")' reports/stale-docs.json > /dev/null; then
      echo "Critical stale documentation found!"
      exit 1
    fi
```

## License

Apache 2.0
