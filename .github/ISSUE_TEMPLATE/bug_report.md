---
name: Bug report
about: Report incorrect or unexpected behavior
title: ""
labels: bug
assignees: ""
---

## Description

A clear description of what's wrong.

## To reproduce

The exact command and flags you ran:

```bash
rustydocs --content-dir ./docs --threshold-days 90 ...
```

- If possible, attach or describe a **minimal repository / file** that
  reproduces the issue (the smallest input that still shows the problem).
- Note your docs format (Markdown / MDX / Hugo / other) if relevant.

## Expected vs actual

**Expected:** what you thought the output / behavior would be.

**Actual:** what happened instead (paste the relevant report output or error).

## Environment

- `rustydocs --version`:
- `git --version`:
- Installed via: prebuilt binary / `go install` / built from source
- OS:
- Was the repo a **full clone** (not shallow / `fetch-depth: 0`)? yes / no
