// Package config provides configuration handling for rustydocs.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// StalenessLevels defines threshold levels for staleness classification.
type StalenessLevels struct {
	Warning  int `json:"warning"`
	Caution  int `json:"caution"`
	Critical int `json:"critical"`
}

// ReusablesConfig defines how to detect and locate reusable components.
type ReusablesConfig struct {
	// Dir is the directory where reusable files are stored
	Dir string `json:"dir"`
	// Patterns are regex patterns to detect reusables in content.
	// Each pattern should have a capture group for the reusable name.
	// Example: `\{\{[<%]\s*reusables/([^\s%>]+)\s*[%>]\}\}` for Hugo shortcodes
	Patterns []string `json:"patterns"`
	// Extensions are file extensions to try when looking for reusable files
	Extensions []string `json:"extensions"`
}

// Config holds the configuration for rustydocs analysis.
type Config struct {
	ThresholdDays int    `json:"threshold_days"`
	ContentDir    string `json:"content_dir"`
	// Profile selects a built-in documentation-tool profile by name (see
	// Profiles). Empty = auto-detect from content_dir (hugo when a layouts/ or
	// themes/ directory, a hugo.{toml,yaml,json} file, or a config/_default/
	// Hugo config is found at or above it, otherwise markdown).
	Profile string `json:"profile"`
	// ResolvedProfile is the profile ApplyProfile selected; ProfileAuto is
	// true when it was auto-detected rather than named explicitly.
	ResolvedProfile Profile `json:"-"`
	ProfileAuto     bool    `json:"-"`
	// ExtensionsFromUser is true when ContentExtensions was already set when
	// ApplyProfile ran, i.e. the allowlist in force came from config or
	// --extensions rather than from a profile default. ApplyProfile also
	// widens the extensions itself under the legacy reusables-dir flow, so the
	// resolved list differing from the profile's is not evidence of a user
	// override; consult this field instead.
	ExtensionsFromUser bool `json:"-"`
	// ContentExtensions is the file-extension allowlist for the walk (empty:
	// from profile). ApplyProfile canonicalises it in place with
	// NormalizeExtensions, so after that call it holds exactly the lowercase,
	// dot-prefixed set the analyzer matches on — which is what the banner, the
	// stderr warnings and the JSON report's content_extensions echo.
	ContentExtensions []string `json:"content_extensions"`
	// HugoRoot is the Hugo project root. When empty, ApplyProfile fills it
	// only if the resolved profile has RootMarkers (today: the hugo profile),
	// using the nearest marker found walking up from ContentDir. Setting it
	// explicitly also forces the hugo profile during auto-detection.
	HugoRoot        string          `json:"hugo_root"`
	ReusablesDir    string          `json:"reusables_dir"` // Deprecated: use Reusables.Dir
	Reusables       ReusablesConfig `json:"reusables"`
	OutputDir       string          `json:"output_dir"`
	ExcludePatterns []string        `json:"exclude_patterns"`
	ExcludeDirs     []string        `json:"exclude_dirs"`
	StalenessLevels StalenessLevels `json:"staleness_levels"`
	FileLevelOnly   bool            `json:"file_level_only"`
	ParagraphLevel  bool            `json:"paragraph_level"`
	Workers         int             `json:"workers"`
	ShowReusables   bool            `json:"show_reusables"` // Show reusables in report (default false)
}

// DefaultConfig returns a new Config with default values. Profile-dependent
// settings (ContentExtensions, Reusables.Patterns, Reusables.Extensions,
// HugoRoot) are left empty here and filled by ApplyProfile.
func DefaultConfig() *Config {
	return &Config{
		ThresholdDays: 90,
		OutputDir:     "./reports",
		StalenessLevels: StalenessLevels{
			Warning:  90,
			Caution:  180,
			Critical: 365,
		},
		Workers: 0, // 0 means use runtime.NumCPU()
	}
}

// LoadConfig loads configuration from a JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Handle backward compatibility: migrate reusables_dir to reusables.dir
	if cfg.ReusablesDir != "" && cfg.Reusables.Dir == "" {
		cfg.Reusables.Dir = cfg.ReusablesDir
	}

	// Content extensions, reusable patterns and reusable extensions left
	// unset are filled from the resolved profile by ApplyProfile, which the
	// caller runs after merging CLI overrides.

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	// Validate ThresholdDays
	if c.ThresholdDays < 0 {
		return fmt.Errorf("threshold_days must be non-negative, got %d", c.ThresholdDays)
	}

	// Validate StalenessLevels
	if c.StalenessLevels.Warning < 0 {
		return fmt.Errorf("staleness_levels.warning must be non-negative, got %d", c.StalenessLevels.Warning)
	}
	if c.StalenessLevels.Caution < 0 {
		return fmt.Errorf("staleness_levels.caution must be non-negative, got %d", c.StalenessLevels.Caution)
	}
	if c.StalenessLevels.Critical < 0 {
		return fmt.Errorf("staleness_levels.critical must be non-negative, got %d", c.StalenessLevels.Critical)
	}

	// Validate Workers
	if c.Workers < 0 {
		return fmt.Errorf("workers must be non-negative, got %d", c.Workers)
	}

	// Validate profile name (empty = auto-detect)
	if c.Profile != "" {
		if _, ok := LookupProfile(c.Profile); !ok {
			return fmt.Errorf("unknown profile %q (valid profiles: %s)", c.Profile, strings.Join(Profiles(), ", "))
		}
	}

	// Validate regex patterns
	for i, pattern := range c.Reusables.Patterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid reusable pattern at index %d (%q): %w", i, pattern, err)
		}
	}

	return nil
}

// Normalize makes the configuration internally coherent. It must be called
// after all sources (file + CLI overrides) have been applied.
//
// The reporting gate (ThresholdDays) and the lowest staleness tier
// (StalenessLevels.Warning) are configured independently, so a section could be
// flagged stale (older than the threshold) yet classified "fresh" (younger than
// the warning tier) — e.g. --threshold-days 30 with the default warning of 90.
// Clamp the warning tier so it never exceeds the threshold: anything past the
// gate is at least "warning". Tiers are then kept monotonic. See #54.
func (c *Config) Normalize() {
	if c.ThresholdDays > 0 && c.StalenessLevels.Warning > c.ThresholdDays {
		c.StalenessLevels.Warning = c.ThresholdDays
	}
	if c.StalenessLevels.Caution < c.StalenessLevels.Warning {
		c.StalenessLevels.Caution = c.StalenessLevels.Warning
	}
	if c.StalenessLevels.Critical < c.StalenessLevels.Caution {
		c.StalenessLevels.Critical = c.StalenessLevels.Caution
	}
}

// GetStalenessClass returns CSS class based on staleness level.
func (c *Config) GetStalenessClass(days int) string {
	switch {
	case days >= c.StalenessLevels.Critical:
		return "critical"
	case days >= c.StalenessLevels.Caution:
		return "caution"
	case days >= c.StalenessLevels.Warning:
		return "warning"
	default:
		return "fresh"
	}
}
