package config

import "testing"

// TestNormalize_ClampsWarningToThreshold pins the #54 fix: a section flagged
// stale at the reporting threshold must never classify as "fresh".
func TestNormalize_ClampsWarningToThreshold(t *testing.T) {
	c := DefaultConfig() // warning 90, caution 180, critical 365
	c.ThresholdDays = 30 // e.g. --threshold-days 30; warning would otherwise stay 90
	c.Normalize()

	if c.StalenessLevels.Warning != 30 {
		t.Errorf("Warning = %d, want clamped to 30", c.StalenessLevels.Warning)
	}
	if got := c.GetStalenessClass(45); got == "fresh" {
		t.Errorf("GetStalenessClass(45) = %q; a stale section must not be 'fresh' (#54)", got)
	}
	if got := c.GetStalenessClass(30); got != "warning" {
		t.Errorf("GetStalenessClass(30) = %q, want warning", got)
	}
}

// TestNormalize_NoClampWhenWarningBelowThreshold: a warning tier already at or
// below the threshold is left untouched.
func TestNormalize_NoClampWhenWarningBelowThreshold(t *testing.T) {
	c := DefaultConfig()
	c.ThresholdDays = 365
	c.Normalize()
	if c.StalenessLevels.Warning != 90 {
		t.Errorf("Warning = %d, want unchanged 90", c.StalenessLevels.Warning)
	}
}

// TestNormalize_KeepsTiersMonotonic: after clamping, warning<=caution<=critical.
func TestNormalize_KeepsTiersMonotonic(t *testing.T) {
	c := &Config{
		ThresholdDays:   10,
		StalenessLevels: StalenessLevels{Warning: 90, Caution: 5, Critical: 1},
	}
	c.Normalize()
	l := c.StalenessLevels
	if l.Warning > l.Caution || l.Caution > l.Critical {
		t.Errorf("tiers not monotonic: %+v", l)
	}
	if l.Warning != 10 {
		t.Errorf("Warning = %d, want clamped to 10", l.Warning)
	}
}

// TestNormalize_ZeroThresholdNoClamp: a zero threshold (unset) does not clamp.
func TestNormalize_ZeroThresholdNoClamp(t *testing.T) {
	c := DefaultConfig()
	c.ThresholdDays = 0
	c.Normalize()
	if c.StalenessLevels.Warning != 90 {
		t.Errorf("Warning = %d, want unchanged when threshold is 0", c.StalenessLevels.Warning)
	}
}
