// Package config provides configuration handling for rustydocs.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	ThresholdDays     int             `json:"threshold_days"`
	ContentDir        string          `json:"content_dir"`
	ContentExtensions []string        `json:"content_extensions"` // File extensions to analyze (default: .md, .markdown, .mdx)
	HugoRoot          string          `json:"hugo_root"`          // Hugo project root (auto-detected if not set)
	ReusablesDir      string          `json:"reusables_dir"`      // Deprecated: use Reusables.Dir
	Reusables         ReusablesConfig `json:"reusables"`
	OutputDir         string          `json:"output_dir"`
	ExcludePatterns   []string        `json:"exclude_patterns"`
	ExcludeDirs       []string        `json:"exclude_dirs"`
	StalenessLevels   StalenessLevels `json:"staleness_levels"`
	FileLevelOnly     bool            `json:"file_level_only"`
	ParagraphLevel    bool            `json:"paragraph_level"`
	Workers           int             `json:"workers"`
	ShowReusables     bool            `json:"show_reusables"` // Show reusables in report (default false)
}

// DefaultConfig returns a new Config with default values.
func DefaultConfig() *Config {
	return &Config{
		ThresholdDays:     90,
		ContentExtensions: []string{".md", ".markdown", ".mdx"},
		OutputDir:         "./reports",
		Reusables: ReusablesConfig{
			// Default patterns for Hugo shortcodes, MDX/JSX components
			Patterns: []string{
				// Hugo shortcodes: {{< name >}}, {{% name %}}, {{< name param >}}, etc.
				`\{\{[<%]\s*([a-zA-Z][\w/-]*)\s*[^%>]*[%>]\}\}`,
				// MDX/JSX components: <Component>, <Component />, <Component prop="val">
				`<([A-Z][a-zA-Z0-9]*)\s*[^>]*/?>`,
			},
			Extensions: []string{".md", ".mdx", ".html"},
		},
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

	// Ensure default patterns if none specified
	if len(cfg.Reusables.Patterns) == 0 {
		cfg.Reusables.Patterns = []string{
			`\{\{[<%]\s*([a-zA-Z][\w/-]*)\s*[^%>]*[%>]\}\}`,
			`<([A-Z][a-zA-Z0-9]*)\s*[^>]*/?>`,
		}
	}

	// Ensure default reusable extensions if none specified
	if len(cfg.Reusables.Extensions) == 0 {
		cfg.Reusables.Extensions = []string{".md", ".mdx", ".html"}
	}

	// Ensure default content extensions if none specified
	if len(cfg.ContentExtensions) == 0 {
		cfg.ContentExtensions = []string{".md", ".markdown", ".mdx"}
	}

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

	// Validate regex patterns
	for i, pattern := range c.Reusables.Patterns {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid reusable pattern at index %d (%q): %w", i, pattern, err)
		}
	}

	return nil
}

// DetectHugoRoot finds the Hugo project root by walking up from contentDir
// looking for a layouts/ directory.
func DetectHugoRoot(contentDir string) string {
	dir := filepath.Clean(contentDir)
	for {
		// Check if layouts/ exists at this level
		layoutsPath := filepath.Join(dir, "layouts")
		if info, err := os.Stat(layoutsPath); err == nil && info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return ""
		}
		dir = parent
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
