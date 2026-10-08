package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each measured iteration is a new analysis run. Distinct paths expose the
// subprocess savings that a benchmark repeatedly hitting one warm cache hides.
// Fixtures have eight linear commits and warm root discovery for both modes.
// Reproduce: go test ./internal/git -run '^$' -bench BenchmarkFileInfoCache -benchmem -benchtime=3x
func BenchmarkFileInfoCache(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("git unavailable")
	}
	lastModifiedTestEnvironment(b)
	for _, n := range []int{32, 256} {
		b.Run(fmt.Sprintf("paths=%d", n), func(b *testing.B) {
			root := b.TempDir()
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				root = resolved
			}
			run := func(args ...string) {
				b.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2024-03-04T10:30:45Z", "GIT_COMMITTER_DATE=2024-03-04T10:30:45Z")
				if out, err := cmd.CombinedOutput(); err != nil {
					b.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			run("init", "-q")
			run("config", "user.name", "Benchmark Author")
			run("config", "user.email", "bench@example.com")
			run("config", "commit.gpgsign", "false")
			paths := make([]string, n)
			for i := range paths {
				paths[i] = filepath.Join(root, fmt.Sprintf("snippet-%04d.md", i))
				if err := os.WriteFile(paths[i], []byte("initial\n"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			run("add", "-A")
			run("commit", "-q", "-m", "initial")
			for commit := 1; commit < 8; commit++ {
				for i := commit; i < n; i += 8 {
					if err := os.WriteFile(paths[i], []byte(fmt.Sprintf("updated %d\n", commit)), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				run("commit", "-q", "-am", fmt.Sprintf("update %d", commit))
			}
			if _, err := GetGitRootForPath(paths[0]); err != nil {
				b.Fatal(err)
			}
			for _, shared := range []bool{false, true} {
				workload := "distinct"
				if shared {
					workload = "shared"
				}
				for _, batch := range []bool{false, true} {
					mode := "memoized-log"
					if batch {
						mode = "batch"
					}
					b.Run(workload+"/"+mode, func(b *testing.B) {
						if batch {
							probe := NewFileInfoCacheWithLastModified()
							if _, err := probe.FileLastModified(paths[0]); err != nil {
								b.Fatal(err)
							}
							if entry := batchForPath(b, probe, paths[0]); entry == nil || len(entry.files) == 0 {
								b.Skip("supported, safe last-modified unavailable")
							}
						}
						b.ReportAllocs()
						b.ResetTimer()
						b.ReportMetric(float64(n), "lookups/run")
						for iteration := 0; iteration < b.N; iteration++ {
							c := NewFileInfoCache()
							if batch {
								c = NewFileInfoCacheWithLastModified()
							}
							for _, path := range paths {
								if shared {
									path = paths[0]
								}
								info, err := c.FileLastModified(path)
								if err != nil || info == nil || !strings.HasPrefix(info.LastAuthor, "Benchmark") {
									b.Fatalf("lookup: %+v, %v", info, err)
								}
							}
							if _, misses := c.Stats(); !shared && misses != n {
								b.Fatalf("distinct workload had %d misses, want %d", misses, n)
							}
						}
					})
				}
			}
		})
	}
}
