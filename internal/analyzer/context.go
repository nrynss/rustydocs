package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
)

// Location never contains a runner-specific absolute path.
type Location struct {
	RepositoryID *string `json:"repository_id"`
	Path         string  `json:"path"`
}

// Repository captures revision and history availability without exporting absolute roots.
type Repository struct {
	ID       string  `json:"id"`
	Path     string  `json:"path"`
	Revision *string `json:"revision"`
	Dirty    *bool   `json:"dirty"`
	Shallow  *bool   `json:"shallow"`
	Status   string  `json:"status"`
}

// physicalPath canonicalizes checkout paths before repository association.
func physicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if phys, err := filepath.EvalSymlinks(path); err == nil {
		path = phys
	}
	return filepath.Clean(path)
}

// relativePath returns a portable location even when volumes cannot be related.
func relativePath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(filepath.Base(path))
	}
	return filepath.ToSlash(rel)
}

// gitOutput captures optional repository metadata without turning absence into a scan failure.
func gitOutput(root string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	data, err := cmd.Output()
	return strings.TrimSpace(string(data)), err == nil
}

// captureContext snapshots repository metadata at analysis time. Export never
// re-queries Git or rereads content, so later checkout changes cannot alter it.
func (r *Results) captureContext(baseDir string) {
	r.Locations = map[string]Location{}
	r.Repositories = []Repository{}
	baseDir = physicalPath(baseDir)
	anchor := baseDir
	if root, err := git.GetGitRootForPath(filepath.Join(baseDir, "_scan_context")); err == nil {
		anchor = root
	} else if r.Config.ProjectRoot != "" {
		anchor = physicalPath(r.Config.ProjectRoot)
	}
	roots := map[string]string{}
	paths := []string{baseDir, r.Config.ContentDir}
	for _, dir := range []string{r.Config.ProjectRoot, r.Config.Reusables.Dir, r.Config.ReusablesDir} {
		if dir != "" {
			paths = append(paths, dir)
		}
	}
	for _, f := range r.Files {
		paths = append(paths, f.Path)
		for _, s := range f.Sections {
			for _, d := range s.Dependencies {
				for _, dep := range d.Files {
					paths = append(paths, dep.Path)
				}
			}
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if _, ok := r.Locations[path]; ok {
			continue
		}
		phys := physicalPath(path)
		// GetGitRootForPath expects a file, so directory context gets a dummy child.
		probe := phys
		if st, err := os.Stat(phys); err == nil && st.IsDir() {
			probe = filepath.Join(phys, "_scan_context")
		}
		root, err := git.GetGitRootForPath(probe)
		if err != nil {
			r.Locations[path] = Location{Path: relativePath(baseDir, phys)}
			continue
		}
		id, ok := roots[root]
		if !ok {
			rel := relativePath(anchor, root)
			id = "repo:" + strings.TrimPrefix(parser.Fingerprint(rel), "sha256:")
			roots[root] = id
			repo := Repository{ID: id, Path: rel, Status: "available"}
			if out, ok := gitOutput(root, "rev-parse", "--verify", "HEAD"); ok {
				repo.Revision = &out
			} else {
				repo.Status = "partial"
			}
			if out, ok := gitOutput(root, "status", "--porcelain=v1", "--untracked-files=normal"); ok {
				dirty := out != ""
				repo.Dirty = &dirty
			} else {
				repo.Status = "partial"
			}
			if out, ok := gitOutput(root, "rev-parse", "--is-shallow-repository"); ok && (out == "true" || out == "false") {
				shallow := out == "true"
				repo.Shallow = &shallow
			} else {
				repo.Status = "partial"
			}
			r.Repositories = append(r.Repositories, repo)
		}
		r.Locations[path] = Location{RepositoryID: &id, Path: relativePath(root, phys)}
	}
	sort.Slice(r.Repositories, func(i, j int) bool { return r.Repositories[i].ID < r.Repositories[j].ID })
}
