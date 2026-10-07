package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedProfileParity(t *testing.T) {
	profiles, err := loadProfileRegistry(profileAssets)
	if err != nil {
		t.Fatal(err)
	}
	// Fingerprints of json.Marshal(Profile) captured from the Go definitions
	// at 8581fad, before extraction. These pin every exported field byte for
	// byte, including descriptions, examples, regexes, slice order and defaults.
	want := []struct{ name, fingerprint string }{
		{"markdown", "92be36cef4ad80150fa79a9f82e6ce765d2dc1407df74b7ed67f35ab9f55fe2e"},
		{"gitbook", "3e364a7dded6c720cb4e01aae59041e97f73bb67aa3c49a0d727949e50a4f581"},
		{"hugo", "c3201db64109b131f0f1116d1e896bd03a06f1b41be94d25ee4e6b872118810a"},
		{"mintlify", "04730b285e427d7691aa0261513a24fb409a1755732d51b386201004b8f0f623"},
		{"starlight", "b0c73bdd0b490f56d39fc54fe23ac21beac8d6a02247437f9cf7029beca8cfd5"},
	}
	if len(profiles) != len(want)+1 || profiles[len(want)].Name != ProfileDocusaurus {
		t.Fatalf("expected original profiles followed by docusaurus, got %v", profiles)
	}
	for i, profile := range profiles[:len(want)] {
		data, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		if profile.Name != want[i].name || fmt.Sprintf("%x", sha256.Sum256(data)) != want[i].fingerprint {
			t.Errorf("profile[%d] changed from pre-extraction registry: %s", i, data)
		}
	}
}

func profileTestAssets(fields string) fstest.MapFS {
	return fstest.MapFS{
		"profiles/registry.json": {Data: []byte(`{"profiles":["markdown"]}`)},
		"profiles/markdown.json": {Data: []byte(`{"name":"markdown","description":"test","content_extensions":[".md"],"resolver":"none"` + fields + `}`)},
	}
}

func TestProfileLoaderDefaultsAndCopies(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         []string
	}{
		{"omitted", "", nil},
		{"null capabilities", `,"parser_capabilities":null`, nil},
		{"null slices", `,"parser_capabilities":{"allowed_explicit_path_extensions":null,"index_file_names":null}`, nil},
		{"empty slices", `,"parser_capabilities":{"allowed_explicit_path_extensions":[],"index_file_names":[]}`, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profiles, err := loadProfileRegistry(profileTestAssets(tc.fields))
			if err != nil {
				t.Fatal(err)
			}
			p := profiles[0]
			want := ParserCapabilities{AllowedExplicitPathExtensions: tc.want, IndexFileNames: tc.want}
			if !reflect.DeepEqual(p.ParserCapabilities, want) || !reflect.DeepEqual(p.clone().ParserCapabilities, want) {
				t.Fatalf("capabilities/clone = %+v, want %+v", p.ParserCapabilities, want)
			}
		})
	}
	assets := profileTestAssets(`,"root_markers":["SUMMARY.md"],"marker_predicates":{"SUMMARY.md":"gitbook-summary"},"reusable_patterns":["(x)"],"reusable_extensions":[".md"],"parser_capabilities":{"allowed_explicit_path_extensions":[".MD"],"index_file_names":["README"]}`)
	profiles, err := loadProfileRegistry(assets)
	if err != nil {
		t.Fatal(err)
	}
	p := profiles[0].clone()
	p.ContentExtensions[0] = ".mutated"
	p.RootMarkers[0] = "mutated"
	p.ReusablePatterns[0] = "mutated"
	p.ReusableExtensions[0] = ".mutated"
	p.ParserCapabilities.AllowedExplicitPathExtensions[0] = ".mutated"
	p.ParserCapabilities.IndexFileNames[0] = "mutated"
	delete(p.markerPredicates, "SUMMARY.md")
	original := profiles[0]
	if original.ContentExtensions[0] != ".md" || original.RootMarkers[0] != "SUMMARY.md" ||
		original.ReusablePatterns[0] != "(x)" || original.ReusableExtensions[0] != ".md" ||
		original.ParserCapabilities.AllowedExplicitPathExtensions[0] != ".MD" ||
		original.ParserCapabilities.IndexFileNames[0] != "README" || original.markerPredicates["SUMMARY.md"] == nil {
		t.Fatal("clone mutated loaded profile")
	}
}

func TestProfileLoaderInvalidDefinitions(t *testing.T) {
	for _, tc := range []struct{ name, fields, want string }{
		{"unknown profile field", `,"surprise":true`, `unknown field "surprise"`},
		{"unknown capability field", `,"parser_capabilities":{"surprise":true}`, `unknown field "surprise"`},
		{"duplicate field", `,"name":"markdown"`, `duplicate JSON field "name"`},
		{"duplicate nested field", `,"parser_capabilities":{"skip_url_captures":true,"skip_url_captures":false}`, `duplicate JSON field "skip_url_captures"`},
		{"invalid capability type", `,"parser_capabilities":{"skip_url_captures":"true"}`, "cannot unmarshal string"},
		{"invalid path base", `,"parser_capabilities":{"path_base_mode":"root-only"}`, "invalid path_base_mode"},
		{"extension no dot", `,"reusable_extensions":["md"]`, "reusable_extensions[0]"},
		{"extension path", `,"reusable_extensions":[".md/other"]`, "reusable_extensions[0]"},
		{"extension whitespace", `,"reusable_extensions":[" .md"]`, "reusable_extensions[0]"},
		{"duplicate extensions", `,"reusable_extensions":[".md",".md"]`, "duplicate value"},
		{"invalid explicit extension", `,"parser_capabilities":{"allowed_explicit_path_extensions":[""]}`, "allowed_explicit_path_extensions[0]"},
		{"index extension", `,"parser_capabilities":{"index_file_names":["index.md"]}`, "index_file_names[0]"},
		{"index slash", `,"parser_capabilities":{"index_file_names":["dir/index"]}`, "index_file_names[0]"},
		{"index backslash", `,"parser_capabilities":{"index_file_names":["dir\\index"]}`, "index_file_names[0]"},
		{"index empty", `,"parser_capabilities":{"index_file_names":[""]}`, "index_file_names[0]"},
		{"index dot", `,"parser_capabilities":{"index_file_names":[".."]}`, "index_file_names[0]"},
		{"duplicate index", `,"parser_capabilities":{"index_file_names":["index","index"]}`, "duplicate value"},
		{"marker traversal", `,"root_markers":["../hugo.toml"]`, "root_markers[0]"},
		{"marker absolute", `,"root_markers":["/hugo.toml"]`, "root_markers[0]"},
		{"marker backslash", `,"root_markers":["config\\hugo.toml"]`, "root_markers[0]"},
		{"marker whitespace", `,"root_markers":[" hugo.toml"]`, "root_markers[0]"},
		{"marker empty segment", `,"root_markers":["config//hugo.toml"]`, "root_markers[0]"},
		{"marker doubled trailing slash", `,"root_markers":["layouts//"]`, "root_markers[0]"},
		{"duplicate marker", `,"root_markers":["hugo.toml","hugo.toml"]`, "duplicate value"},
		{"unlisted predicate marker", `,"marker_predicates":{"hugo.toml":"mintlify-config"}`, "must name a listed file marker"},
		{"directory predicate", `,"root_markers":["layouts/"],"marker_predicates":{"layouts/":"mintlify-config"}`, "must name a listed file marker"},
		{"unknown predicate", `,"root_markers":["docs.json"],"marker_predicates":{"docs.json":"unknown"}`, `unknown predicate "unknown"`},
		{"duplicate predicate", `,"root_markers":["docs.json"],"marker_predicates":{"docs.json":"mintlify-config","docs.json":"mintlify-config"}`, `duplicate JSON field "docs.json"`},
		{"invalid regex", `,"reusable_patterns":["("]`, "invalid regex"},
		{"missing capture", `,"reusable_patterns":["x"]`, "exactly one capture group"},
		{"extra capture", `,"reusable_patterns":["(x)(y)"]`, "exactly one capture group"},
		{"empty pattern", `,"reusable_patterns":[""]`, "reusable_patterns[0]"},
		{"duplicate pattern", `,"reusable_patterns":["(x)","(x)"]`, "duplicate value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadProfileRegistry(profileTestAssets(tc.fields))
			if err == nil || !strings.Contains(err.Error(), "profiles/markdown.json:") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want markdown.json context and %q", err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ name, value, want string }{
		{"name mismatch", `{"name":"hugo","description":"test","content_extensions":[".md"],"resolver":"none"}`, "does not match registry name"},
		{"missing resolver", `{"name":"markdown","description":"test","content_extensions":[".md"]}`, "invalid resolver"},
		{"unknown resolver", `{"name":"markdown","description":"test","content_extensions":[".md"],"resolver":"unknown"}`, "invalid resolver"},
		{"empty description", `{"name":"markdown","content_extensions":[".md"],"resolver":"none"}`, "description must not be empty"},
		{"missing extensions", `{"name":"markdown","description":"test","resolver":"none"}`, "content_extensions must not be empty"},
		{"invalid content extension", `{"name":"markdown","description":"test","content_extensions":["md"],"resolver":"none"}`, "content_extensions[0]"},
		{"trailing object", `{} {}`, "trailing data"},
		{"trailing junk", `{} junk`, "trailing data"},
		{"null profile", `null`, "expected JSON object"},
		{"array profile", `[]`, "expected JSON object"},
		{"truncated profile", `{"name":`, "EOF"},
		{"malformed array", `{"root_markers":[}`, "invalid character"},
		{"malformed key", `{"name"}`, "invalid character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := profileTestAssets("")
			assets["profiles/markdown.json"].Data = []byte(tc.value)
			_, err := loadProfileRegistry(assets)
			if err == nil || !strings.Contains(err.Error(), "profiles/markdown.json:") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want markdown.json context and %q", err, tc.want)
			}
		})
	}
}

func TestProfileLoaderInvalidRegistry(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"unknown field", `{"profiles":["markdown"],"surprise":true}`, `unknown field "surprise"`},
		{"duplicate profiles", `{"profiles":["markdown","markdown"]}`, `duplicate profile "markdown"`},
		{"duplicate registry field", `{"profiles":["markdown"],"profiles":["markdown"]}`, `duplicate JSON field "profiles"`},
		{"unknown profile", `{"profiles":["markdown","missing"]}`, "profiles/missing.json"},
		{"missing fallback", `{"profiles":["hugo"]}`, `missing fallback profile "markdown"`},
		{"empty registry", `{"profiles":[]}`, "profiles must not be empty"},
		{"null names", `{"profiles":null}`, "profiles must not be empty"},
		{"noncanonical name", `{"profiles":["Markdown"]}`, "invalid profile name"},
		{"name traversal", `{"profiles":["../markdown"]}`, "invalid profile name"},
		{"reserved name", `{"profiles":["markdown","registry"]}`, "invalid profile name"},
		{"trailing object", `{"profiles":["markdown"]} {}`, "trailing data"},
		{"null registry", `null`, "expected JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := profileTestAssets("")
			assets["profiles/registry.json"].Data = []byte(tc.value)
			_, err := loadProfileRegistry(assets)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	for _, extra := range []string{"profiles/unknown.json", "profiles/readme.txt", "profiles/nested/profile.json"} {
		t.Run(extra, func(t *testing.T) {
			assets := profileTestAssets("")
			assets[extra] = &fstest.MapFile{Data: []byte(`{}`)}
			if _, err := loadProfileRegistry(assets); err == nil || !strings.Contains(err.Error(), "unlisted profile asset") {
				t.Fatalf("error = %v, want unlisted asset", err)
			}
		})
	}
	for _, missing := range []string{"profiles/registry.json", "profiles/markdown.json"} {
		assets := profileTestAssets("")
		delete(assets, missing)
		if _, err := loadProfileRegistry(assets); err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("error = %v, want missing %s", err, missing)
		}
	}
}

// A file system can read the manifest but fail to enumerate the directory.
type profileReadDirErrorFS struct{ fs.FS }

func (profileReadDirErrorFS) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, fs.ErrPermission
}

func TestProfileLoaderReadDirErrorAndStartupFailure(t *testing.T) {
	if _, err := loadProfileRegistry(profileReadDirErrorFS{profileTestAssets("")}); err == nil || !strings.Contains(err.Error(), "profiles: permission denied") {
		t.Fatalf("error = %v, want contextual directory error", err)
	}
	defer func() {
		message := fmt.Sprint(recover())
		if !strings.Contains(message, "invalid embedded profile registry (developer error)") || !strings.Contains(message, "profiles/registry.json") {
			t.Fatalf("panic = %s, want developer error with asset context", message)
		}
	}()
	mustLoadProfileRegistry(fstest.MapFS{})
}

func TestProfileManifestOrderAndPredicates(t *testing.T) {
	// Exercise actual marker detection, including predicate bindings, rather
	// than just asserting the order of the decoded profile list.
	root := t.TempDir()
	content := filepath.Join(root, "docs")
	if err := os.MkdirAll(content, 0755); err != nil {
		t.Fatal(err)
	}
	markers := []struct{ name, contents, winner string }{
		{"package.json", `{"dependencies":{"@astrojs/starlight":"*"}}`, ProfileStarlight},
		{"docs.json", `{"navigation":{}}`, ProfileMintlify},
		{"hugo.toml", "", ProfileHugo},
		{"SUMMARY.md", "# Summary\n\n* [Page](page.md)\n", ProfileGitBook},
	}
	for _, marker := range markers {
		if err := os.WriteFile(filepath.Join(root, marker.name), []byte(marker.contents), 0644); err != nil {
			t.Fatal(err)
		}
		p, foundRoot, ok := detectProfile(content, nil)
		if !ok || p.Name != marker.winner || foundRoot != root {
			t.Fatalf("after adding %s: profile/root/found = %s/%s/%v, want %s/%s/true", marker.name, p.Name, foundRoot, ok, marker.winner, root)
		}
	}
	// A nearer marker beats all the older profiles at the parent level.
	if err := os.WriteFile(filepath.Join(content, "astro.config.mjs"), []byte(`starlight()`), 0644); err != nil {
		t.Fatal(err)
	}
	if p, foundRoot, ok := detectProfile(content, nil); !ok || p.Name != ProfileStarlight || foundRoot != content {
		t.Fatalf("nearer marker lost: %s/%s/%v", p.Name, foundRoot, ok)
	}
	// Reordering registry data changes only tie precedence, with no code edit.
	assets := profileTestAssets("")
	assets["profiles/registry.json"].Data = []byte(`{"profiles":["markdown","second","first"]}`)
	for _, name := range []string{"first", "second"} {
		assets["profiles/"+name+".json"] = &fstest.MapFile{Data: []byte(fmt.Sprintf(`{"name":%q,"description":"test","content_extensions":[".md"],"root_markers":["shared.json"],"resolver":"path"}`, name))}
	}
	profiles, err := loadProfileRegistry(assets)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "shared.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	if p, _, ok := detectNearest(content, profiles, nil); !ok || p.Name != "second" {
		t.Fatalf("manifest order lost to filename order: %s, found %v", p.Name, ok)
	}
}
