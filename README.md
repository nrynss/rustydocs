# rustydocs

[![Go Reference](https://pkg.go.dev/badge/github.com/nrynss/rustydocs.svg)](https://pkg.go.dev/github.com/nrynss/rustydocs)

Find stale documentation using git history. Analyzes your documentation at the section level to identify content that hasn't been updated recently.

## Features

- **Section-level analysis**: Uses `git blame` to analyze staleness per section, not just per file
- **Works on any Markdown repo out of the box**: the default `markdown` profile analyzes `.md`/`.markdown` files with no setup
- **Tool profiles**: `hugo` and `mintlify` profiles (both auto-detected) add MDX support and include tracking — Hugo shortcodes / JSX components, and Mintlify snippets resolved by path; more profiles are on the way (see [Profiles](#profiles))
- **Component tracking**: Under the `hugo` profile, detects Hugo shortcodes (`{{< >}}`, `{{% %}}`) and JSX/MDX components (`<Component>`); under `mintlify`, both `<Snippet file="foo.mdx" />` includes and MDX imports (`import X from "/snippets/x.mdx"` rendered as `<X />`) — and folds their freshness into the section that uses them. Component imports (`.jsx`/`.js`/`.css`) are deliberately skipped, since a restyle must not make every page that uses them look fresh
- **Scans documentation, not tooling**: dot-directories, vendored and build trees, nested standalone repositories (but not submodules) and git-ignored files are excluded by default (`--no-default-excludes` to opt out) — on a real 4,000-file docs repo that is an 8x speedup and 1,078 fewer spurious *unknown* rows
- **Parallel processing**: Analyzes multiple files concurrently using goroutines
- **Dual output**: Generates both Markdown and HTML reports
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
| `mintlify` | `.md`, `.mdx`              | `docs.json` (current) or `mint.json` (legacy) file whose contents look like a Mintlify config | `<Snippet file="…" />` (either quote style), resolved as a **path** under `snippets/` / `_snippets/` or the project root |
| `hugo`     | `.md`, `.markdown`, `.mdx` | `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config | Hugo shortcodes + MDX/JSX components, resolved via `layouts/shortcodes` and `themes/*/layouts/shortcodes` |

**Auto-detection.** When no profile is named, rustydocs walks up from
`content_dir` one directory at a time looking for the profiles' root markers;
the marker nearest to `content_dir` wins. A `docs.json` or `mint.json` file
selects `mintlify`; a `layouts/` or `themes/` directory, a
`hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config — `hugo.*`
or `config.*` under that directory — selects `hugo` (the config-file and
`themes/` markers matter for fresh clones of theme-based sites, since git does
not track an empty `layouts/` directory). A nested docs tree therefore wins
over a marker further up: a Mintlify `docs.json` inside a repo that also has a
`layouts/` at its root selects `mintlify`.

The Mintlify markers are checked by **content** as well as by name: a
`docs.json` or `mint.json` selects the profile only when it parses as a JSON
object carrying a recognisably Mintlify key (`navigation`, `theme`, `colors`,
`logo`, `favicon`, `tabs`, `anchors`, or a `$schema` mentioning Mintlify). A
file that merely has the name — some other tool's `docs.json`, or a malformed
one — is ignored, and detection carries on up the tree. Only when both a Hugo
and a Mintlify marker sit in the *same* directory does registry order decide,
and there **`hugo` wins**: `layouts/` and `hugo.toml` are unambiguous evidence,
and picking `mintlify` would silently switch shortcode tracing off on a Hugo
site that happens to ship a `docs.json`. Pass `--profile mintlify` to override
that tie.

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

# --project-root alone never changes the profile: on a repo with a docs.json
# this is still a mintlify run.
rustydocs --content-dir ./docs --project-root .
```

Or in `config.json`: `"profile": "hugo"` (empty string = auto-detect).

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
| `profile`              | Documentation profile (`markdown`, `mintlify`, `hugo`); empty = auto-detect | (auto-detect)   |
| `content_dir`          | Directory containing documentation files           | (required)                   |
| `content_extensions`   | File extensions to analyze                         | from profile                 |
| `project_root`         | Project root reusables resolve against — the Hugo site root, or the Mintlify project root snippet paths are relative to (auto-detected for any profile with root markers). Never influences which profile is selected. CLI: `--project-root` | (auto-detect)   |
| `hugo_root`            | **Deprecated** spelling of `project_root`. On its own it still supplies the root, with a deprecation warning telling you to rename it; when `project_root` (or `--project-root`) is set too, that one wins and `hugo_root` is *ignored*, with a warning saying so. Unlike `project_root` it keeps its legacy side effect: it selects the `hugo` profile when no marker is found | (auto-detect)          |
| `output_dir`           | Output directory for reports                       | `./reports`                  |
| `reusables.dir`        | Directory containing reusable component files      | (optional)                   |
| `reusables.patterns`   | Regex patterns to detect reusables (capture group) | from profile (`hugo`: shortcodes + JSX; `mintlify`: `<Snippet file>` + imported components; `markdown`: none) |
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
   `git check-ignore` for the whole run, so nested `.gitignore` files,
   negations, `core.excludesFile` and the index are all honoured exactly. A
   *tracked* file is never dropped, however the patterns read.

A content tree that is not a git repository still works: the name rules apply,
the ignore query is skipped, and its files are reported *unknown* as they always
were. The content root itself is never pruned, so `--content-dir .` at a
repository root, or pointing straight at a dot-directory, does what you meant.

Measured on a production Mintlify site, this took a run from 4,032 files in
48.7 s with 1,078 files reported as having no git history, to 551 files in 7.5 s
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

**Only `.md` and `.mdx` imports are followed.** A `.jsx`, `.js` or `.css`
import, or one of a package (`@mintlify/components`), is detected and
deliberately **skipped**. This is not an oversight — folding an include's date
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
  --profile NAME          Documentation profile: hugo, markdown, mintlify (default: auto-detect)
  --project-root PATH     Project root that reusable references resolve against (Hugo site
                          root, Mintlify docs root); default: detected from the profile's
                          markers. It never selects a profile on its own, so pair it with
                          --profile on a project whose markers are absent. Config-file
                          spelling: "project_root" (deprecated: "hugo_root")
  --list-profiles         List built-in profiles and exit
  --file-level-only       Skip section-level analysis (faster)
  --paragraph-level       Analyze at paragraph level (more granular)
  --workers INT           Number of parallel workers (default: number of CPUs)
  --version               Show version and exit
```

## Output

Three reports are generated in the output directory:

| File | Format | Use Case |
|------|--------|----------|
| `stale-docs.json` | JSON | CI/CD pipelines, GitHub Actions, tooling |
| `stale-docs.md` | Markdown | Pull requests, git-friendly review |
| `stale-docs.html` | HTML | Visual dashboard, sharing |

### JSON Report (`stale-docs.json`)

Machine-readable format for CI/CD integration:

```json
{
  "version": "1.0",
  "generated_at": "2025-01-22T10:30:00Z",
  "config": {
    "threshold_days": 90,
    "content_dir": "docs",
    "profile": "markdown",
    "profile_auto": true,
    "content_extensions": [".md", ".markdown"],
    "staleness_levels": { "warning": 90, "caution": 180, "critical": 365 }
  },
  "summary": {
    "total_files": 150,
    "stale_files": 45,
    "stale_files_pct": 30.0,
    "total_sections": 620,
    "stale_sections": 89,
    "stale_sections_pct": 14.35
  },
  "files": [
    {
      "path": "deployment/github-app.md",
      "days_stale": 178,
      "stale_sections": 2,
      "sections": [
        {
          "title": "Prerequisites",
          "start_line": 29,
          "days_stale": 845,
          "level": "critical"
        }
      ]
    }
  ]
}
```

`config` records the run that produced the artifact: `profile` is the resolved
profile name, `profile_auto` is `true` when it was auto-detected rather than
passed with `--profile`, and `content_extensions` is the allowlist that was
actually scanned — so a consumer can tell "nothing is stale" from "the run
never looked at these files".

### Markdown Report (`stale-docs.md`)

```markdown
# Stale Documentation Report
Generated: 2025-12-10 | Threshold: 90 days

## Summary
- **Files scanned:** 150
- **Files with stale content:** 45 (30%)
- **Stale sections:** 89 (14%)

## deployment/github-app.md
| Line | Section       | Last Updated | Days Stale | Author |
| ---- | ------------- | ------------ | ---------- | ------ |
| L29  | Prerequisites | 2022-08-18   | 845        | @john  |
```

### HTML Report (`stale-docs.html`)

- Color-coded staleness levels (yellow/orange/red)
- Collapsible file sections
- Quick navigation sidebar

## How It Works

1. **Resolves** a profile (`--profile`, or auto-detected from the content dir) and **scans** the files whose extensions it lists
2. **Parses** content to identify sections by headers (`#`, `##`, `###`)
3. **Runs** `git blame` concurrently to get per-line modification dates
4. **Detects** reusable components (Hugo shortcodes, JSX — `hugo` profile; `<Snippet file>` includes — `mintlify` profile) and checks their freshness
5. **Calculates** section staleness based on the oldest line in each section
6. **Generates** reports in JSON, Markdown, and HTML

## GitHub Actions

Use the JSON output in CI/CD pipelines:

```yaml
- name: Check documentation freshness
  run: |
    rustydocs --content-dir ./docs --output-dir ./reports

    # Fail if critical stale content exists
    if jq -e '.files[] | select(.sections[]?.level == "critical")' reports/stale-docs.json > /dev/null; then
      echo "Critical stale documentation found!"
      exit 1
    fi
```

## License

Apache 2.0
