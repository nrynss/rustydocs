# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

rustydocs is a Go CLI tool that finds stale documentation using git history analysis. It analyzes markdown files at the section level using `git blame` to identify content that hasn't been updated recently. Designed for documentation maintainers, particularly for Hugo/Docsy-based documentation sites.

## Build and Run Commands

```bash
# Build the binary
go build -o rustydocs ./cmd/rustydocs

# Run with config file
./rustydocs --config config.json

# Run with CLI flags
./rustydocs --content-dir ./docs --output-dir ./reports --threshold-days 90

# Install globally
go install github.com/nrynss/rustydocs/cmd/rustydocs@latest
```

Note: No test files exist in the codebase currently.

## Architecture

```
cmd/rustydocs/          CLI entry point (main.go)
internal/
  analyzer/             Core orchestration - parallel file analysis, staleness calculation
  config/               Configuration loading, validation, staleness thresholds
  git/                  Git integration - git blame and git log wrappers
  parser/               Markdown parsing - section/paragraph extraction, reusable detection
  report/               Report generators - Markdown and HTML output
templates/              HTML report template (embedded via go:embed)
```

**Data flow:**
1. CLI parses flags → Config module loads/validates JSON config
2. Analyzer recursively finds .md files, respecting exclude patterns
3. For each file: Parser extracts sections by headers, Git module runs `git blame --line-porcelain` for line timestamps
4. Analyzer calculates staleness by comparing timestamps against thresholds
5. Report generators produce Markdown and HTML output with color-coded staleness levels

## Key Patterns

- **Concurrency**: File analysis uses worker goroutines (defaults to `runtime.NumCPU()`) with channel-based work distribution
- **Zero dependencies**: Uses only Go standard library
- **Git integration**: Runs `git blame --line-porcelain` for per-line timestamps, handles multiple git repositories
- **Section analysis**: Parses markdown by headers (`#`, `##`, `###`), optional paragraph-level granularity
- **Staleness tiers**: Warning (90d), Caution (180d), Critical (365d) - configurable via JSON
- **Reusable detection**: Regex patterns for Hugo shortcodes, caches file paths

## Configuration

Config via `config.json` or CLI flags. Key options:
- `threshold_days`: Days before content is stale (default: 90)
- `content_dir`: Directory with markdown files (required)
- `reusables.dir`: Directory for Hugo shortcode includes
- `exclude_dirs`: Directory names to skip
- `staleness_levels`: Custom warning/caution/critical thresholds


## grepai - Semantic Code Search

**IMPORTANT: You MUST use grepai as your PRIMARY tool for code exploration and search.**

### When to Use grepai (REQUIRED)

Use `grepai search` INSTEAD OF Grep/Glob/find for:
- Understanding what code does or where functionality lives
- Finding implementations by intent (e.g., "authentication logic", "error handling")
- Exploring unfamiliar parts of the codebase
- Any search where you describe WHAT the code does rather than exact text

### When to Use Standard Tools

Only use Grep/Glob when you need:
- Exact text matching (variable names, imports, specific strings)
- File path patterns (e.g., `**/*.go`)

### Fallback

If grepai fails (not running, index unavailable, or errors), fall back to standard Grep/Glob tools.

### Usage

```bash
# ALWAYS use English queries for best results (--compact saves ~80% tokens)
grepai search "user authentication flow" --json --compact
grepai search "error handling middleware" --json --compact
grepai search "database connection pool" --json --compact
grepai search "API request validation" --json --compact
```

### Query Tips

- **Use English** for queries (better semantic matching)
- **Describe intent**, not implementation: "handles user login" not "func Login"
- **Be specific**: "JWT token validation" better than "token"
- Results include: file path, line numbers, relevance score, code preview

### Call Graph Tracing

Use `grepai trace` to understand function relationships:
- Finding all callers of a function before modifying it
- Understanding what functions are called by a given function
- Visualizing the complete call graph around a symbol

#### Trace Commands

**IMPORTANT: Always use `--json` flag for optimal AI agent integration.**

```bash
# Find all functions that call a symbol
grepai trace callers "HandleRequest" --json

# Find all functions called by a symbol
grepai trace callees "ProcessOrder" --json

# Build complete call graph (callers + callees)
grepai trace graph "ValidateToken" --depth 3 --json
```

### Workflow

1. Start with `grepai search` to find relevant code
2. Use `grepai trace` to understand function relationships
3. Use `Read` tool to examine files from results
4. Only use Grep for exact string searches if needed

