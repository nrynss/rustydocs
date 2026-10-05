# Per-run git lookup memoization

The old `perf/memoize-git-lookups` commit `0ed6541` already landed through
`e97fb94` (PR #67). Current main `426b8bf` contains its shared cache, concurrency
deduplication, negative caching, nil fallback, and parser/analyzer wiring. This
follow-up fixes an actual cache identity bug: a file symlink whose parent belongs
to a different repository could reuse the target's history or error. The cache
now includes the resolved parent directory as well as the resolved target.

The new key conservatively retains distinct entries for file aliases in distinct
directories, even within one repository. Whole-checkout directory symlinks still
share entries. The map mutex is released before `sync.Once` runs a lookup;
results are copied with each caller's `Path`. No file-info cache survives an analysis run; Git-root discovery has a separate
package-level directory cache.
The checkout, history, and symlinks must stay stable during analysis; the analyzer
does not pin HEAD or provide a snapshot.

## Reproduce

Requires Python 3.12+, Git, Go, and hyperfine. Run from the repository root:

```sh
python3 benchmarks/memoize_git_lookups.py /tmp/rustydocs-memo-benchmark \
  --baseline 426b8bf --candidate b9e0f3c --pages 300 --workers 8 --runs 5
```

Use a new output directory. `--prepare-only` builds binaries and verifies results;
`--time-only` subsequently times those saved binaries without rebuilding. This
helps keep tests, builds, and other heavy work outside the timing window.

Three binaries are built from archived commits with `-buildvcs=false`:

- `main`: current main `426b8bf`, including the already merged memoization.
- `candidate`: correctness fix `b9e0f3c` on current main.
- `uncached-control`: the same main source, with only
  `fileInfoCache := git.NewFileInfoCache()` in `AnalyzeWithProgress` replaced by
  `var fileInfoCache *git.FileInfoCache`. The existing nil-receiver fallback
  performs direct lookups. This measures the value of the existing optimization
  on current profiles, rather than comparing incompatible historical binaries.

Each corpus has 300 committed content files and one uncommitted file. Mintlify,
Starlight MDX, GitBook, and Hugo each reference three shared committed includes
per page (900 references); the includes are outside the scanned content tree.
Markdown has no references and exercises unique file-level lookups. Git history
is one commit dated 2024-01-01, with a fixed author. Runs use eight workers, one
warmup and five measured repetitions, normal section analysis, and all three
report formats. hyperfine runs commands sequentially; filesystem/process caches
are warm. This is a synthetic sharing-heavy workload, not a universal speedup.

Before timing, the harness verifies 301 scanned files and exactly one unknown
history file; reusable cases must have three resolved histories. JSON, Markdown,
and HTML match across the three builds after removing only generation timestamps.
stdout also matches after removing progress frames; stderr matches exactly.
A deliberately mismatched extension verifies zero-file scans and warning parity.
Age/day values, authors, reusable histories, and unknown flags are not normalized.
Existing tests cover cached errors, nil results, concurrent same/distinct paths,
independent runs, defensive copies, and path spelling. The new regression compares
cross-repository file symlink lookups with direct Git queries in both orders and
fails against the old key.

## Measured result

Measured 2026-10-05 on Apple M3 Pro, Darwin arm64, Go 1.27.1, Git 2.54.0.
Raw hyperfine samples and exact ref/command metadata are in
[`results/2026-10-05/`](results/2026-10-05/metadata.json). Times below are wall
seconds, mean ± sample standard deviation; five runs per command.

| Corpus | Uncached control | Current main | Fixed candidate | Existing cache speedup (control/main) |
| --- | ---: | ---: | ---: | ---: |
| Mintlify snippets | 12.641 ± 0.298 | 2.532 ± 0.079 | 2.551 ± 0.127 | 4.99× |
| Starlight MDX imports | 12.666 ± 0.256 | 2.464 ± 0.059 | 2.478 ± 0.047 | 5.14× |
| GitBook includes, initial | 12.486 ± 0.251 | 2.476 ± 0.133 | 3.389 ± 1.574 | 5.04× |
| Hugo shortcodes | 13.626 ± 0.332 | 2.713 ± 0.132 | 2.601 ± 0.119 | 5.02× |
| Markdown, unique/no includes | 2.502 ± 0.104 | 2.563 ± 0.086 | 2.574 ± 0.118 | 0.98× |

GitBook's initial candidate samples ranged from 2.402 to 6.137 seconds. A repeat
after the full suite, with candidate then main (reversed order), measured
candidate **2.592 ± 0.047 s**, main **2.702 ± 0.123 s**. Both initial and repeat
samples are retained; the outlier's cause is unproven. Reproduce that comparison
against the saved artifacts with:

```sh
python3 benchmarks/memoize_git_lookups.py /tmp/rustydocs-memo-benchmark \
  --time-only --repeat-profile gitbook
```

The existing cache delivers roughly 5× improvement for these sharing-heavy
corpora. The unique-file control shows a 2.4% cache cost in the means, with
overlapping variation; hyperfine flagged statistical outliers for its uncached
command. The correctness fix has no consistent slowdown across the repeated
measurements: Mintlify/Starlight/Markdown means increase by less than 1%, Hugo
and the GitBook repeat decrease by about 4%, all with overlapping variation.
These are limited local measurements, not a guarantee about other repositories
or hosts. Historical September timings in the changelog are not evidence for
this run. This change fixes cache identity; it does not newly add the already
merged optimization.
