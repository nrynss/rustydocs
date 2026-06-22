package config

import (
	"os"
	"path/filepath"
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
	// Deprecated reusables_dir should migrate to reusables.dir; omitted
	// content/reusable extensions should fall back to defaults.
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
	if len(cfg.ContentExtensions) == 0 {
		t.Error("expected default content_extensions")
	}
	if len(cfg.Reusables.Extensions) == 0 {
		t.Error("expected default reusable extensions")
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

func TestDetectHugoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(root, "content", "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectHugoRoot(content); got != root {
		t.Errorf("DetectHugoRoot = %q, want %q", got, root)
	}

	// A tree with no layouts/ anywhere up to the root returns "".
	if got := DetectHugoRoot(t.TempDir()); got != "" {
		t.Errorf("DetectHugoRoot(no layouts) = %q, want \"\"", got)
	}
}
