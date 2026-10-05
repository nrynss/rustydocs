#!/usr/bin/env python3
"""Reproducible CLI benchmark; requires Python 3, git, Go, and hyperfine.

Builds archived refs, including a clearly labeled cache-disabled baseline. All
fixtures, builds, reports and hyperfine JSON stay in the supplied new directory.
"""
import argparse
from datetime import datetime, timezone
import io
import json
import os
import platform
from pathlib import Path
import re
import shlex
import subprocess
import tarfile


def run(args, **kwargs):
    kwargs.setdefault('timeout', 1800)
    return subprocess.run(args, check=True, **kwargs)


def write(root, name, text):
    path = root / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def build(repo, ref, destination, disable=False):
    destination.mkdir()
    archive = subprocess.check_output(['git', 'archive', ref], cwd=repo)
    with tarfile.open(fileobj=io.BytesIO(archive)) as source:
        source.extractall(destination, filter='data')
    if disable:
        path = destination / 'internal/analyzer/analyzer.go'
        text = path.read_text()
        seam = 'fileInfoCache := git.NewFileInfoCache()'
        assert text.count(seam) == 1, 'cache-disable seam changed; inspect manually'
        path.write_text(text.replace(seam, 'var fileInfoCache *git.FileInfoCache'))
    binary = destination / 'rustydocs'
    run(['go', 'build', '-buildvcs=false', '-o', str(binary), './cmd/rustydocs'], cwd=destination)
    return binary


def fixture(root, profile, pages):
    root.mkdir()
    extension = '.mdx' if profile in ('mintlify', 'starlight') else '.md'
    for i in range(pages):
        if profile == 'mintlify':
            refs = '\n'.join(f'<Snippet file="/shared/s{j}.mdx" />' for j in range(3))
        elif profile == 'starlight':
            refs = '\n'.join(f'import S{j} from "../shared/s{j}.mdx";' for j in range(3))
            refs += '\n\n' + '\n'.join(f'<S{j} />' for j in range(3))
        elif profile == 'gitbook':
            refs = '\n'.join('{% include "../shared/s' + str(j) + '.md" %}' for j in range(3))
        elif profile == 'hugo':
            refs = '\n'.join('{{< s' + str(j) + ' >}}' for j in range(3))
        else:
            refs = 'Standalone text without reusable references.'
        write(root, f'content/page{i:04}{extension}', '# Page\n\n' + refs + '\n')
    for j in range(3):
        name = f'layouts/shortcodes/s{j}.html' if profile == 'hugo' else f'shared/s{j}{extension}'
        write(root, name, 'Shared body\n')
    run(['git', 'init', '-q', str(root)])
    run(['git', '-C', str(root), 'add', '.'])
    env = dict(os.environ, GIT_AUTHOR_NAME='Benchmark', GIT_AUTHOR_EMAIL='bench@example.invalid',
               GIT_COMMITTER_NAME='Benchmark', GIT_COMMITTER_EMAIL='bench@example.invalid',
               GIT_AUTHOR_DATE='2024-01-01T00:00:00Z', GIT_COMMITTER_DATE='2024-01-01T00:00:00Z')
    run(['git', '-C', str(root), 'commit', '-qm', 'controlled history'], env=env)
    # One content file with unknown history exercises warning and unknown output.
    write(root, f'content/uncommitted{extension}', '# Unknown\n\nUncommitted body\n')


def normalise(text):
    # Only wall-clock generation fields differ between independent CLI runs.
    text = re.sub(r'Generated: \d{4}-\d\d-\d\d \d\d:\d\d', 'Generated: <time>', text)
    return re.sub(r'Generated on \d{4}-\d\d-\d\d \d\d:\d\d', 'Generated on <time>', text)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path, help='new directory for artifacts')
    parser.add_argument('--baseline', default='426b8bf')
    parser.add_argument('--candidate', default='HEAD')
    parser.add_argument('--pages', type=int, default=300)
    parser.add_argument('--workers', type=int, default=8)
    parser.add_argument('--runs', type=int, default=5)
    parser.add_argument('--prepare-only', action='store_true')
    parser.add_argument('--time-only', action='store_true')
    parser.add_argument('--repeat-profile', choices=('mintlify', 'starlight', 'gitbook', 'hugo', 'markdown'),
                        help='repeat the candidate/main pair in reversed order')
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[1]
    out = args.directory.resolve()
    labels = ('uncached-control', 'main', 'candidate')
    if not args.time_only:
        out.mkdir()  # Refuse to replace existing results.
        binaries = [build(repo, args.baseline, out / 'uncached-control', True),
                    build(repo, args.baseline, out / 'main'),
                    build(repo, args.candidate, out / 'candidate')]
        metadata = {'prepared_at_utc': datetime.now(timezone.utc).isoformat(),
                    'cpu': subprocess.check_output(['sysctl', '-n', 'machdep.cpu.brand_string'], text=True).strip() if platform.system() == 'Darwin' else platform.processor(),
                    'hyperfine': subprocess.check_output(['hyperfine', '--version'], text=True).strip(),
                    'baseline': subprocess.check_output(['git', 'rev-parse', args.baseline], cwd=repo, text=True).strip(),
                    'candidate': subprocess.check_output(['git', 'rev-parse', args.candidate], cwd=repo, text=True).strip(),
                    'pages': args.pages, 'workers': args.workers, 'runs': args.runs,
                    'go': subprocess.check_output(['go', 'version'], text=True).strip(),
                    'git': subprocess.check_output(['git', '--version'], text=True).strip(),
                    'uname': subprocess.check_output(['uname', '-a'], text=True).strip()}
        commands = {}
        for profile in ('mintlify', 'starlight', 'gitbook', 'hugo', 'markdown'):
            root = out / ('fixture-' + profile)
            fixture(root, profile, args.pages)
            report = out / ('report-' + profile)
            commands[profile] = []
            reference = None
            for label, binary in zip(labels, binaries):
                command = [str(binary), '--profile', profile, '--content-dir', str(root / 'content'),
                           '--output-dir', str(report), '--workers', str(args.workers)]
                if profile != 'markdown':
                    command += ['--project-root', str(root)]
                proc = run(command, capture_output=True, text=True)
                data = json.loads((report / 'stale-docs.json').read_text())
                assert data['summary']['total_files'] == args.pages + 1, data['summary']
                assert data['summary']['files_missing_history'] == 1, data['summary']
                if profile != 'markdown':
                    assert len(data['reusables']) == 3, data.get('reusables')
                    assert all(r.get('last_updated') for r in data['reusables']), data['reusables']
                data.pop('generated_at')
                reports = [normalise((report / name).read_text()) for name in ('stale-docs.md', 'stale-docs.html')]
                stdout = re.sub(r'\n\[.*?\]\s+\d+% \(\d+/\d+ files\)', '', proc.stdout)
                comparison = [data, reports, stdout, proc.stderr]
                if reference is None:
                    reference = comparison
                else:
                    assert comparison == reference, f'{profile}: {label} output changed beyond generation timestamp'
                commands[profile].append(shlex.join(command))
            # Deliberately mismatched extension must scan zero and warn identically.
            zero = [run(shlex.split(cmd) + ['--extensions', '.nomatch'], capture_output=True, text=True) for cmd in commands[profile]]
            assert all('Files scanned: 0' in p.stdout and 'Warning:' in p.stderr for p in zero)
            assert all((p.stdout, p.stderr) == (zero[0].stdout, zero[0].stderr) for p in zero)
        metadata['commands'] = commands
        metadata['verification'] = 'All 3 formats (generation timestamp removed), stdout (progress frames removed)/stderr, one unknown file and zero-match warnings agree for all cases.'
        (out / 'metadata.json').write_text(json.dumps(metadata, indent=2) + '\n')
        print(metadata['verification'], flush=True)
    if args.prepare_only:
        return
    metadata = json.loads((out / 'metadata.json').read_text())
    for profile, commands in metadata['commands'].items():
        if args.repeat_profile and profile != args.repeat_profile:
            continue
        case_labels = labels
        suffix = ''
        if args.repeat_profile:
            commands = [commands[2], commands[1]]
            case_labels = ('candidate', 'main')
            suffix = '-repeat'
        command = ['hyperfine', '--warmup', '1', '--runs', str(metadata['runs']),
                   '--export-json', str(out / (profile + suffix + '.json'))]
        for label, shell in zip(case_labels, commands):
            command += ['--command-name', label, shell]
        run(command)


if __name__ == '__main__':
    main()
