package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type lastModifiedEntry struct {
	once  sync.Once
	files map[string]FileInfo // nil means this repository must use git log
}

type fileInfoRootEntry struct {
	once sync.Once
	root string
	err  error
}

func (c *FileInfoCache) lookupFileLastModified(path string) (*FileInfo, error) {
	if c.lastModified {
		if info, ok := c.batchFileLastModified(path); ok {
			return info, nil
		}
	}
	return GetFileLastModified(path)
}

// Select the repository from the original parent before resolving the target,
// just like GetFileLastModified. Coalesce cold directory-root probes too, so
// concurrent distinct files in one directory do not spawn duplicate probes.
func (c *FileInfoCache) rootFor(path string) (string, error) {
	key := normaliseCachePath(filepath.Dir(path))
	c.mu.Lock()
	if c.directories == nil {
		c.directories = make(map[string]*fileInfoRootEntry)
	}
	entry := c.directories[key]
	if entry == nil {
		entry = &fileInfoRootEntry{}
		c.directories[key] = entry
	}
	c.mu.Unlock()
	entry.once.Do(func() { entry.root, entry.err = GetGitRootForPath(path) })
	return entry.root, entry.err
}

func (c *FileInfoCache) batchFileLastModified(path string) (*FileInfo, bool) {
	root, err := c.rootFor(path)
	if err != nil {
		return nil, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, false
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		// Missing and deleted paths still have meaningful git-log history.
		return nil, false
	}
	stat, err := os.Stat(abs)
	if err != nil || !stat.Mode().IsRegular() {
		// Directories (including gitlinks) are aggregate pathspec queries.
		return nil, false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false
	}
	rel = filepath.ToSlash(rel)
	if strings.ContainsAny(rel, ":*?[") {
		// The baseline intentionally passes an ordinary Git pathspec, not a
		// literal path. A filename with magic may select several other files.
		return nil, false
	}
	c.mu.Lock()
	if c.repositories == nil {
		c.repositories = make(map[string]*lastModifiedEntry)
	}
	entry := c.repositories[root]
	if entry == nil {
		entry = &lastModifiedEntry{}
		c.repositories[root] = entry
	}
	c.mu.Unlock()
	entry.once.Do(func() { entry.files = c.loadLastModified(root) })
	info, ok := entry.files[rel]
	if !ok {
		// An absent HEAD entry could be untracked, deleted then recreated, or
		// an unusual spelling. Only the baseline can distinguish these cases.
		return nil, false
	}
	info.Path = path
	return &info, true
}

func (c *FileInfoCache) gitOutput(root, input string, args ...string) ([]byte, error) {
	if c.runGit != nil {
		return c.runGit(root, input, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(input)
	return cmd.Output()
}

// Fail closed: a repository batch is published only after every command and
// every record validates. Failures never replace the baseline's error or
// no-history result and are remembered for this run, rather than retried by
// every worker. No Git-version heuristic is used for this experimental command.
func (c *FileInfoCache) loadLastModified(root string) map[string]FileInfo {
	if !c.batchHistorySafe(root) {
		return nil
	}
	out, err := c.gitOutput(root, "", "last-modified", "-r", "-z", "HEAD")
	if err != nil {
		return nil
	}
	paths, err := parseLastModified(out)
	if err != nil || len(paths) == 0 {
		return nil
	}
	unique := make(map[string]bool)
	for _, oid := range paths {
		unique[oid] = true
	}
	oids := make([]string, 0, len(unique))
	for oid := range unique {
		oids = append(oids, oid)
	}
	sort.Strings(oids)
	// Using log's formatter preserves %an (unmapped author), %ai (author
	// time with offset), and output encoding exactly as the baseline does.
	// stdin avoids an argument-length limit for repositories with many commits.
	out, err = c.gitOutput(root, strings.Join(oids, "\n")+"\n", "log",
		"--no-walk=unsorted", "--format=%H%x00%an%x00%ai%x00", "--stdin")
	if err != nil {
		return nil
	}
	metadata, err := parseLastModifiedMetadata(out, unique)
	if err != nil {
		return nil
	}
	files := make(map[string]FileInfo, len(paths))
	for path, oid := range paths {
		files[path] = metadata[oid]
	}
	return files
}

func (c *FileInfoCache) batchHistorySafe(root string) bool {
	// GIT_CONFIG makes `git config --list` inspect that file, while the
	// history commands still honor GIT_CONFIG_COUNT and related overrides.
	// Without inspecting the same effective config, keep the baseline path.
	if _, set := os.LookupEnv("GIT_CONFIG"); set {
		return false
	}
	for _, name := range []string{"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS"} {
		if _, set := os.LookupEnv(name); set {
			return false
		}
	}
	// log.showRoot=false, log.follow, and diff settings can change the
	// baseline's history selection. Conservatively keep all log/diff overrides
	// on the baseline, including config injected through Git's environment.
	out, err := c.gitOutput(root, "", "config", "--null", "--list")
	if err != nil {
		return false
	}
	for _, record := range bytes.Split(out, []byte{0}) {
		key := strings.ToLower(strings.SplitN(string(record), "\n", 2)[0])
		if strings.HasPrefix(key, "log.") || strings.HasPrefix(key, "diff.") {
			return false
		}
	}
	// Path-limited log uses history simplification and date ordering across
	// merges; last-modified uses its own per-path generation/date queue. A
	// linear ancestry has only one next parent, so these choices coincide.
	out, err = c.gitOutput(root, "", "rev-list", "--min-parents=2", "--max-count=1", "HEAD")
	if err != nil || len(out) != 0 {
		return false
	}
	out, err = c.gitOutput(root, "", "replace", "-l")
	if err != nil || len(out) != 0 {
		return false
	}
	out, err = c.gitOutput(root, "", "rev-parse", "--is-shallow-repository", "--git-path", "info/grafts")
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 || lines[0] != "false" {
		return false
	}
	grafts := lines[1]
	if !filepath.IsAbs(grafts) {
		grafts = filepath.Join(root, grafts)
	}
	_, err = os.Stat(grafts)
	return os.IsNotExist(err)
}

func validLastModifiedOID(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	for _, ch := range oid {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return strings.Trim(oid, "0") != ""
}

func parseLastModified(out []byte) (map[string]string, error) {
	if len(out) == 0 || out[len(out)-1] != 0 {
		return nil, fmt.Errorf("last-modified output is not NUL terminated")
	}
	paths := make(map[string]string)
	for _, record := range bytes.Split(out[:len(out)-1], []byte{0}) {
		oid, path, ok := strings.Cut(string(record), "\t")
		if !ok || !validLastModifiedOID(oid) || path == "" ||
			strings.HasPrefix(path, "/") ||
			filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path ||
			path == "." || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("invalid last-modified record")
		}
		if _, exists := paths[path]; exists {
			return nil, fmt.Errorf("duplicate last-modified path")
		}
		paths[path] = oid
	}
	return paths, nil
}

func parseLastModifiedMetadata(out []byte, wanted map[string]bool) (map[string]FileInfo, error) {
	metadata := make(map[string]FileInfo, len(wanted))
	for len(out) != 0 {
		fields := make([]string, 3)
		for i := range fields {
			field, rest, ok := bytes.Cut(out, []byte{0})
			if !ok {
				return nil, fmt.Errorf("incomplete last-modified metadata")
			}
			fields[i], out = string(field), rest
		}
		if len(out) == 0 || out[0] != '\n' {
			return nil, fmt.Errorf("invalid last-modified metadata terminator")
		}
		out = out[1:]
		oid, author, date := fields[0], fields[1], fields[2]
		if !wanted[oid] || metadata[oid].LastCommit != "" || strings.ContainsAny(author, "\r\n") {
			return nil, fmt.Errorf("unexpected last-modified metadata")
		}
		stamp, err := time.Parse("2006-01-02 15:04:05 -0700", date)
		if err != nil {
			return nil, err
		}
		metadata[oid] = FileInfo{LastCommit: oid, LastAuthor: author, LastModified: stamp}
	}
	if len(metadata) != len(wanted) {
		return nil, fmt.Errorf("missing last-modified metadata")
	}
	return metadata, nil
}
