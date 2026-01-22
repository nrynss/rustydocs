# rustydocs

Find stale documentation using git history. Analyzes your documentation at the section level to identify content that hasn't been updated recently.

## Features

- **Section-level analysis**: Uses `git blame` to analyze staleness per section, not just per file
- **Scans all text files**: Automatically processes all text files in your content directory (binary files are skipped)
- **MDX support**: Works with Markdown, MDX, and any text-based documentation format
- **Component tracking**: Detects Hugo shortcodes (`{{< >}}`, `{{% %}}`) and JSX/MDX components (`<Component>`)
- **Parallel processing**: Analyzes multiple files concurrently using goroutines
- **Dual output**: Generates both Markdown and HTML reports
- **Zero dependencies**: Uses only Go standard library
- **Configurable thresholds**: Set custom staleness levels (warning, caution, critical)

## Installation

```bash
# Build from source (includes version info)
make build

# Or install directly
go install github.com/nrynss/rustydocs/cmd/rustydocs@latest

# Check version
./rustydocs --version
```

## Quick Start

```bash
# Run with default settings (90 day threshold)
rustydocs --content-dir ./docs --output-dir ./reports

# Use a config file
rustydocs --config config.json

# Custom threshold
rustydocs --content-dir ./docs --threshold-days 180
```

## Configuration

Create a `config.json` file:

```json
{
  "threshold_days": 90,
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
| `content_dir`          | Directory containing documentation files           | (required)                   |
| `hugo_root`            | Hugo project root (auto-detected if not set)       | (auto-detect)                |
| `output_dir`           | Output directory for reports                       | `./reports`                  |
| `reusables.dir`        | Directory containing reusable component files      | (optional)                   |
| `reusables.patterns`   | Regex patterns to detect reusables (capture group) | Hugo shortcodes + JSX        |
| `exclude_patterns`     | Glob patterns to exclude files                     | `[]`                         |
| `exclude_dirs`         | Directory names to exclude entirely                | `[]`                         |
| `staleness_levels`     | Thresholds for warning/caution/critical            | 90/180/365                   |
| `paragraph_level`      | Analyze at paragraph level (more granular)         | false                        |

### Hugo Shortcode Tracing

When you use Hugo shortcodes like `{{< alert >}}`, rustydocs automatically:

1. Finds the shortcode template in `layouts/shortcodes/alert.html`
2. Parses the template for data references (`readFile`, `partial`, `.Site.Data`)
3. Tracks freshness of both the template and any data files it uses

This means if your content uses `{{< reusables/warning >}}` and that shortcode reads from `data/reusables/warning.md`, the staleness check includes the data file's last modification date.

The `hugo_root` is auto-detected by walking up from `content_dir` until finding a `layouts/` directory.

### Default Component Patterns

The tool automatically detects:

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

1. **Scans** all text files in your content directory (binary files automatically skipped)
2. **Parses** content to identify sections by headers (`#`, `##`, `###`)
3. **Runs** `git blame` concurrently to get per-line modification dates
4. **Detects** reusable components (Hugo shortcodes, JSX) and checks their freshness
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
