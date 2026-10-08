#!/usr/bin/env python3
"""Emit a GitHub Actions output deciding whether changed files need CI."""
import os
import subprocess


def needs_ci(path):
    # Embedded assets and test fixtures can be Markdown, HTML or JSON.
    if path.startswith(("cmd/", "internal/", "scripts/")):
        return True
    if path.startswith(("docs/", "benchmarks/results/", ".github/ISSUE_TEMPLATE/")):
        return False
    if path in ("LICENSE", "NOTICE"):
        return False
    if path.endswith((".md", ".mdx", ".rst", ".adoc")):
        return False
    # New build inputs and unfamiliar files should run CI by default.
    return True


def main():
    base = os.environ.get("CI_BASE", "")
    head = os.environ.get("CI_HEAD", "")
    if not base or not head or set(base) == {"0"}:
        print("run_ci=true")
        return
    comparison = [base, head]
    if os.environ.get("CI_EVENT") == "pull_request":
        comparison = [base + "..." + head]
    try:
        # Include old and new paths on renames so moving code to docs still runs.
        changed = subprocess.check_output(
            ["git", "diff", "--no-renames", "--name-only", "-z", *comparison, "--"],
            stderr=subprocess.PIPE,
        )
    except subprocess.CalledProcessError:
        # A force-push can make the previous SHA unavailable in this checkout.
        print("run_ci=true")
        return
    paths = (os.fsdecode(path) for path in changed.split(b"\0") if path)
    print("run_ci=" + str(any(needs_ci(path) for path in paths)).lower())


if __name__ == "__main__":
    main()
