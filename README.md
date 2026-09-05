# rustydocs

[![Go Reference](https://pkg.go.dev/badge/github.com/nrynss/rustydocs.svg)](https://pkg.go.dev/github.com/nrynss/rustydocs)

Find stale documentation using git history. Analyzes your documentation at the section level to identify content that hasn't been updated recently.

## Features

- **Section-level analysis**: Uses `git blame` to analyze staleness per section, not just per file
- **Works on any Markdown repo out of the box**: the default `markdown` profile analyzes `.md`/`.markdown` files with no setup
- **Tool profiles**: a `hugo` profile (auto-detected) adds MDX support and Hugo shortcode / JSX component tracking; more profiles are on the way (see [Profiles](#profiles))
- **Component tracking**: Under the `hugo` profile, detects Hugo shortcodes (`{{< >}}`, `{{% %}}`) and JSX/MDX components (`<Component>`) and folds their freshness into the section that uses them
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
`reusables.patterns`, `reusables.dir`, `hugo_root`) always wins.

| Profile    | Extensions                 | Root marker         | Reusable detection                                   |
| ---------- | -------------------------- | ------------------- | ---------------------------------------------------- |
| `markdown` | `.md`, `.markdown`         | none                | **off** (plain CommonMark/GFM has no include mechanism) |
| `hugo`     | `.md`, `.markdown`, `.mdx` | `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config | Hugo shortcodes + MDX/JSX components, resolved via `layouts/shortcodes` and `themes/*/layouts/shortcodes` |

**Auto-detection.** When no profile is named, rustydocs walks up from
`content_dir` one directory at a time looking for the profiles' root markers;
the marker nearest to `content_dir` wins (a `layouts/` or `themes/` directory,
a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config — `hugo.*`
or `config.*` under that directory — selects `hugo`; the config-file and
`themes/` markers matter for fresh clones of theme-based sites, since git does
not track an empty `layouts/` directory). If nothing is found, the `markdown`
profile is used. An explicit `hugo_root` also selects `hugo`, and a Hugo site
matching none of these markers must pass `--profile hugo` or set `hugo_root`.
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
rustydocs --list-profiles                        # print the built-in profiles
rustydocs --content-dir ./docs --profile hugo    # force a profile
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
  "hugo_root": "src/hugo/docsy",
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
| `profile`              | Documentation profile (`markdown`, `hugo`); empty = auto-detect | (auto-detect)   |
| `content_dir`          | Directory containing documentation files           | (required)                   |
| `content_extensions`   | File extensions to analyze                         | from profile                 |
| `hugo_root`            | Hugo project root (auto-detected for the `hugo` profile) | (auto-detect)          |
| `output_dir`           | Output directory for reports                       | `./reports`                  |
| `reusables.dir`        | Directory containing reusable component files      | (optional)                   |
| `reusables.patterns`   | Regex patterns to detect reusables (capture group) | from profile (`hugo`: shortcodes + JSX; `markdown`: none) |
| `exclude_patterns`     | Glob patterns to exclude files                     | `[]`                         |
| `exclude_dirs`         | Directory names to exclude entirely                | `[]`                         |
| `staleness_levels`     | Thresholds for warning/caution/critical            | 90/180/365                   |
| `paragraph_level`      | Analyze at paragraph level (more granular)         | false                        |

### Hugo Shortcode Tracing

Under the `hugo` profile, when you use Hugo shortcodes like `{{< alert >}}`, rustydocs automatically:

1. Finds the shortcode template in `layouts/shortcodes/alert.html`, falling
   back to each theme's `themes/<theme>/layouts/shortcodes/alert.html` (a
   project template of the same name wins, matching Hugo's own lookup order)
2. Parses the template for data references (`readFile`, `partial`, `.Site.Data`)
3. Tracks freshness of both the template and any data files it uses

This means if your content uses `{{< reusables/warning >}}` and that shortcode reads from `data/reusables/warning.md`, the staleness check includes the data file's last modification date.

The `hugo_root` is auto-detected by walking up from `content_dir` (no further than the enclosing git repository root, though a submodule `content/` is walked through into its parent repository) until finding a `layouts/` or `themes/` directory, a `hugo.{toml,yaml,json}` file, or a `config/_default/` Hugo config; finding one is also what selects the `hugo` profile.

### Default Component Patterns

The `hugo` profile detects (the `markdown` profile detects nothing unless you set `reusables.patterns`):

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
  --exclude-dirs STRING   Comma-separated directories to exclude
  --extensions STRING     Comma-separated documentation extensions to analyze (default: from profile)
  --profile NAME          Documentation profile: hugo, markdown (default: auto-detect)
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
4. **Detects** reusable components (Hugo shortcodes, JSX — `hugo` profile) and checks their freshness
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
