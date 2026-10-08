#!/usr/bin/env python3
"""Smoke-test a built image against mounted full/shallow Git histories."""
import datetime
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def run(args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs)


def main():
    image, platform = sys.argv[1:]
    docker = ["docker", "run", "--rm", "--platform", platform]
    version = run(docker + [image, "--version"])
    assert "rustydocs " in version and "commit:" in version and "built:" in version, version
    expected_version = os.environ.get("RUSTYDOCS_EXPECTED_VERSION")
    expected_commit = os.environ.get("RUSTYDOCS_EXPECTED_COMMIT")
    if expected_version:
        assert version.splitlines()[0] == "rustydocs " + expected_version, version
    if expected_commit:
        assert "commit: " + expected_commit in version, version
    git_version = run(docker + ["--entrypoint", "git", image, "--version"])
    print(platform, git_version.strip(), version.strip(), flush=True)

    # Keep fixtures under the checkout, which desktop Docker VMs share.
    fixture_dir = os.environ.get("RUSTYDOCS_TEST_TMPDIR", str(Path.cwd()))
    with tempfile.TemporaryDirectory(prefix=".rustydocs-container-", dir=fixture_dir) as tmp:
        base = Path(tmp)
        repo = base / "repo"
        docs = repo / "docs"
        docs.mkdir(parents=True)
        git = ["git", "-C", str(repo)]
        run(git + ["init", "-q"])
        run(git + ["config", "user.name", "Container test"])
        run(git + ["config", "user.email", "container-test@example.invalid"])
        page = docs / "page.md"
        page.write_text("# Old\n\nOld documentation.\n\n")
        run(git + ["add", "."])
        env = dict(os.environ, GIT_AUTHOR_DATE="2020-01-01T00:00:00Z",
                   GIT_COMMITTER_DATE="2020-01-01T00:00:00Z")
        run(git + ["commit", "-qm", "Old documentation"], env=env)
        page.write_text(page.read_text() + "# Fresh\n\nFresh documentation.\n")
        run(git + ["add", "."])
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        env.update(GIT_AUTHOR_DATE=now, GIT_COMMITTER_DATE=now)
        run(git + ["commit", "-qm", "Fresh section"], env=env)
        (docs / "untracked.md").write_text("# Unknown\n\nNo committed history.\n")

        # Exercise ownership differing from the default image user. Only /src is
        # trusted by the image; the input is read-only and reports are separate.
        for directory in [base, repo, docs, repo / ".git"]:
            directory.chmod(0o755)
        mount = ["--mount", f"type=bind,src={repo},dst=/src,readonly"]
        batch = run(docker + mount + ["--entrypoint", "git", image,
                    "last-modified", "-r", "-z", "HEAD"])
        assert "docs/page.md" in batch, batch

        def scan(source, name, user=None):
            output = base / name
            output.mkdir()
            output.chmod(0o777)
            args = docker + ["--mount", f"type=bind,src={source},dst=/src,readonly",
                             "--mount", f"type=bind,src={output},dst=/reports"]
            if user:
                args += ["--user", user]
            run(args + [image, "--content-dir", "/src/docs",
                        "--output-dir", "/reports", "--threshold-days", "90"])
            reports = list(output.glob("*.json"))
            assert len(reports) == 1, list(output.iterdir())
            assert list(output.glob("*.html")) and list(output.glob("*.md"))
            # Reports are deliberately mode 0600. Native Linux bind mounts keep
            # the container UID, so read as the same user that wrote the report.
            data = run(args + ["--entrypoint", "cat", image,
                               "/reports/" + reports[0].name])
            return json.loads(data)

        for name, user in [("default-user", None),
                           ("host-user", f"{os.getuid()}:{os.getgid()}")]:
            result = scan(repo, name, user)
            assert result["version"] == "2.0", result
            assert result["coverage"]["failed_files"] == 0, result
            assert result["summary"]["files_missing_history"] == 1, result
            assert result["repositories"][0]["shallow"] is False, result
            files = {f["content_path"]: f for f in result["files"]}
            assert files["page.md"]["history_status"] == "available", files
            sections = {s["title"]: s for s in files["page.md"]["sections"]}
            assert sections["Old"]["is_stale"] is True, sections
            assert sections["Fresh"]["is_stale"] is False, sections
            assert sections["Old"]["own_last_change"]["date"].startswith("2020-01-01"), sections
            assert files["untracked.md"]["history_status"] == "missing", files
            assert files["untracked.md"]["sections"][0]["age_days"] is None, files

        shallow = base / "shallow"
        run(["git", "clone", "-q", "--depth", "1", repo.as_uri(), str(shallow)])
        shallow.chmod(0o755)
        result = scan(shallow, "shallow-reports")
        assert result["repositories"][0]["shallow"] is True, result
        assert any(d["code"] == "shallow_history" for d in result["diagnostics"]), result
        print(platform, "mounted full/shallow history, section ages, unknown history, "
              "default/host users and report writes verified", flush=True)


if __name__ == "__main__":
    main()
