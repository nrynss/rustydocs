package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}

	neg := DefaultConfig()
	neg.ThresholdDays = -1
	if err := neg.Validate(); err == nil {
		t.Error("expected error for negative threshold_days")
	}

	badPat := DefaultConfig()
	badPat.Reusables.Patterns = []string{"([unclosed"}
	if err := badPat.Validate(); err == nil {
		t.Error("expected error for invalid reusable regex")
	}
}

func TestLoadConfig_DefaultsAndMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// Deprecated reusables_dir should migrate to reusables.dir. Omitted
	// content/reusable extensions stay empty until ApplyProfile fills them
	// from the resolved profile.
	body := `{"content_dir":"docs","reusables_dir":"shared"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}
	if cfg.Reusables.Dir != "shared" {
		t.Errorf("reusables_dir not migrated: got %q", cfg.Reusables.Dir)
	}
	if len(cfg.ContentExtensions) != 0 || len(cfg.Reusables.Extensions) != 0 || len(cfg.Reusables.Patterns) != 0 {
		t.Errorf("LoadConfig must not bake profile defaults: exts=%v reusable exts=%v patterns=%v",
			cfg.ContentExtensions, cfg.Reusables.Extensions, cfg.Reusables.Patterns)
	}

	cfg.ContentDir = filepath.Join(dir, "docs") // no layouts/ anywhere above
	if err := cfg.ApplyProfile(); err != nil {
		t.Fatalf("ApplyProfile: %v", err)
	}
	if cfg.ResolvedProfile.Name != ProfileMarkdown || !cfg.ProfileAuto {
		t.Errorf("resolved %q auto=%v, want markdown auto-detected", cfg.ResolvedProfile.Name, cfg.ProfileAuto)
	}
	if len(cfg.ContentExtensions) == 0 {
		t.Error("expected content_extensions from profile")
	}
	// Legacy reusables_dir with no patterns keeps the pre-profile hugo pattern
	// list and reusable extensions so the reusables-dir flow still works.
	if len(cfg.Reusables.Patterns) == 0 {
		t.Error("expected legacy reusables_dir to enable the hugo pattern list")
	}
	if len(cfg.Reusables.Extensions) == 0 {
		t.Error("expected default reusable extensions")
	}
}

func TestLoadConfig_UnknownProfileRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"content_dir":"docs","profile":"bogus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "markdown") {
		t.Fatalf("expected unknown-profile error naming valid profiles, got %v", err)
	}
}

func TestGetStalenessClass(t *testing.T) {
	c := DefaultConfig() // warning 90, caution 180, critical 365
	cases := []struct {
		days int
		want string
	}{
		{10, "fresh"},
		{90, "warning"},
		{200, "caution"},
		{400, "critical"},
	}
	for _, tc := range cases {
		if got := c.GetStalenessClass(tc.days); got != tc.want {
			t.Errorf("GetStalenessClass(%d) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

// TestDetectRoot_HugoLayoutsMarker covers the Hugo profile's root detection:
// DetectRoot with the hugo profile's RootMarkers (layouts/ and themes/ plus
// the hugo.* and config/_default/ config files). The trailing slash means the
// layouts marker is a directory: a regular file named layouts is not a Hugo
// marker. The other markers are exercised in TestDetectRoot_HugoConfigMarkers
// and TestDetectRoot_HugoThemeAndSplitConfigMarkers (profile_test.go).
func TestDetectRoot_HugoLayoutsMarker(t *testing.T) {
	markers := mustProfile(ProfileHugo).RootMarkers
	if !reflect.DeepEqual(markers, wantHugoMarkers) {
		t.Fatalf("hugo RootMarkers = %v, want %v", markers, wantHugoMarkers)
	}

	root := t.TempDir()
	content := filepath.Join(root, "content", "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}

	// A regular file named layouts is not the marker.
	if err := os.WriteFile(filepath.Join(root, "layouts"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DetectRoot(content, markers); got != "" {
		t.Errorf("DetectRoot(file named layouts) = %q, want \"\"", got)
	}
	if err := os.Remove(filepath.Join(root, "layouts")); err != nil {
		t.Fatal(err)
	}

	// A layouts/ directory is.
	if err := os.MkdirAll(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectRoot(content, markers); got != root {
		t.Errorf("DetectRoot(layouts/) = %q, want %q", got, root)
	}

	// A tree with no layouts/ anywhere up to the root returns "".
	if got := DetectRoot(t.TempDir(), markers); got != "" {
		t.Errorf("DetectRoot(no layouts) = %q, want \"\"", got)
	}
}
