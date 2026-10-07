package config

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// Built-ins are shipped in the binary; this is not a runtime config source.
// Embed the directory so an unlisted asset is caught by registry validation.
//
//go:embed profiles
var profileAssets embed.FS

// registry.json fixes tie precedence independently of file names. The nearest
// marker still wins; only same-directory ties use this order. Markdown is the
// fallback, followed by GitBook, Hugo, Mintlify, Starlight, then Docusaurus.
var builtinProfiles = mustLoadProfileRegistry(profileAssets)

// Predicate algorithms stay in Go. Definitions can reuse an existing binding
// without changes to the loader, parser or CLI.
var markerPredicateBindings = map[string]markerPredicate{
	"gitbook-summary":   isGitBookSummary,
	"mintlify-config":   isMintlifyConfig,
	"starlight-config":  isStarlightConfig,
	"starlight-package": isStarlightPackage,
}

// Private wire types keep the public user-config JSON API unchanged. Missing
// capability fields use Go zero values: false flags, shared-first path bases,
// nil explicit extensions (unrestricted), and nil index names ("index"). JSON
// null slices also mean nil; [] is preserved as an explicitly empty list.
type profileRegistryDefinition struct {
	Profiles []string `json:"profiles"`
}

type profileDefinition struct {
	Name               string                       `json:"name"`
	Description        string                       `json:"description"`
	IncludeExample     string                       `json:"include_example"`
	ParserCapabilities parserCapabilitiesDefinition `json:"parser_capabilities"`
	ContentExtensions  []string                     `json:"content_extensions"`
	RootMarkers        []string                     `json:"root_markers"`
	MarkerPredicates   map[string]string            `json:"marker_predicates"`
	ReusablePatterns   []string                     `json:"reusable_patterns"`
	ReusableExtensions []string                     `json:"reusable_extensions"`
	Resolver           Resolver                     `json:"resolver"`
	ImportMap          bool                         `json:"import_map"`
}

type parserCapabilitiesDefinition struct {
	MaskFencedChunking            bool         `json:"mask_fenced_chunking"`
	SkipFencedCaptures            bool         `json:"skip_fenced_captures"`
	SkipURLCaptures               bool         `json:"skip_url_captures"`
	SkipAliasShapedIncludes       bool         `json:"skip_alias_shaped_includes"`
	PathCapturesOnly              bool         `json:"path_captures_only"`
	StripCaptureFragments         bool         `json:"strip_capture_fragments"`
	AllowedExplicitPathExtensions []string     `json:"allowed_explicit_path_extensions"`
	PathBaseMode                  PathBaseMode `json:"path_base_mode"`
	IndexFileNames                []string     `json:"index_file_names"`
}

func mustLoadProfileRegistry(assets fs.FS) []Profile {
	profiles, err := loadProfileRegistry(assets)
	if err != nil {
		panic(fmt.Sprintf("config: invalid embedded profile registry (developer error): %v", err))
	}
	return profiles
}

// loadProfileRegistry accepts an FS so validation can be exercised without
// modifying the embedded assets. Each manifest name maps to exactly one file.
func loadProfileRegistry(assets fs.FS) ([]Profile, error) {
	const manifest = "profiles/registry.json"
	var registry profileRegistryDefinition
	if err := decodeProfileAsset(assets, manifest, &registry); err != nil {
		return nil, err
	}
	if len(registry.Profiles) == 0 {
		return nil, fmt.Errorf("%s: profiles must not be empty", manifest)
	}
	listed := make(map[string]bool, len(registry.Profiles))
	for _, name := range registry.Profiles {
		if !profileNamePattern.MatchString(name) || name == "registry" {
			return nil, fmt.Errorf("%s: invalid profile name %q", manifest, name)
		}
		if listed[name] {
			return nil, fmt.Errorf("%s: duplicate profile %q", manifest, name)
		}
		listed[name] = true
	}
	if !listed[ProfileMarkdown] {
		return nil, fmt.Errorf("%s: missing fallback profile %q", manifest, ProfileMarkdown)
	}
	entries, err := fs.ReadDir(assets, "profiles")
	if err != nil {
		return nil, fmt.Errorf("profiles: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || (!listed[strings.TrimSuffix(name, ".json")] && name != "registry.json") || path.Ext(name) != ".json" {
			return nil, fmt.Errorf("profiles/%s: unlisted profile asset", name)
		}
	}
	profiles := make([]Profile, 0, len(registry.Profiles))
	for _, name := range registry.Profiles {
		file := "profiles/" + name + ".json"
		var definition profileDefinition
		if err := decodeProfileAsset(assets, file, &definition); err != nil {
			return nil, err
		}
		if definition.Name != name {
			return nil, fmt.Errorf("%s: name %q does not match registry name %q", file, definition.Name, name)
		}
		profile, err := definition.profile()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func decodeProfileAsset(assets fs.FS, file string, target any) error {
	data, err := fs.ReadFile(assets, file)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	// encoding/json otherwise accepts duplicate keys (last wins), even when
	// DisallowUnknownFields is enabled. Reject ambiguity before schema decoding.
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := checkJSONValue(dec); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("%s: trailing data after JSON object", file)
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%s: expected JSON object", file)
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

// checkJSONValue walks objects and arrays to reject duplicate object fields at
// every level. Token validates syntax; the typed decoder validates field types.
func checkJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	fields := map[string]bool{}
	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if fields[name] {
				return fmt.Errorf("duplicate JSON field %q", name)
			}
			fields[name] = true
		}
		if err := checkJSONValue(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

var profileNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var profileExtensionPattern = regexp.MustCompile(`^\.[A-Za-z0-9][A-Za-z0-9_-]*$`)

func (d profileDefinition) profile() (Profile, error) {
	if strings.TrimSpace(d.Description) == "" {
		return Profile{}, fmt.Errorf("description must not be empty")
	}
	if len(d.ContentExtensions) == 0 {
		return Profile{}, fmt.Errorf("content_extensions must not be empty")
	}
	for _, list := range []struct {
		field  string
		values []string
	}{
		{"content_extensions", d.ContentExtensions},
		{"reusable_extensions", d.ReusableExtensions},
		{"parser_capabilities.allowed_explicit_path_extensions", d.ParserCapabilities.AllowedExplicitPathExtensions},
	} {
		if err := validateProfileStrings(list.field, list.values, func(value string) bool {
			return profileExtensionPattern.MatchString(value)
		}); err != nil {
			return Profile{}, err
		}
	}
	switch d.Resolver {
	case ResolverNone, ResolverHugo, ResolverPath:
	default:
		return Profile{}, fmt.Errorf("invalid resolver %q", d.Resolver)
	}
	c := d.ParserCapabilities
	switch c.PathBaseMode {
	case PathBaseSharedFirst, PathBasePageOnly:
	default:
		return Profile{}, fmt.Errorf("parser_capabilities: invalid path_base_mode %q", c.PathBaseMode)
	}
	if err := validateProfileStrings("parser_capabilities.index_file_names", c.IndexFileNames, func(value string) bool {
		return value != "" && value != "." && value != ".." && strings.TrimSpace(value) == value &&
			!strings.ContainsAny(value, "/\\.:\x00\r\n\t")
	}); err != nil {
		return Profile{}, err
	}
	if err := validateProfileStrings("root_markers", d.RootMarkers, func(value string) bool {
		name := strings.TrimSuffix(value, "/")
		return fs.ValidPath(name) && name != "." && strings.TrimSpace(value) == value &&
			!strings.ContainsAny(name, "\\:\x00\r\n\t")
	}); err != nil {
		return Profile{}, err
	}
	predicates := make(map[string]markerPredicate, len(d.MarkerPredicates))
	for marker, binding := range d.MarkerPredicates {
		found := false
		for _, listed := range d.RootMarkers {
			found = found || marker == listed
		}
		if !found || strings.HasSuffix(marker, "/") {
			return Profile{}, fmt.Errorf("marker_predicates: %q must name a listed file marker", marker)
		}
		predicate, ok := markerPredicateBindings[binding]
		if !ok {
			return Profile{}, fmt.Errorf("marker_predicates[%q]: unknown predicate %q", marker, binding)
		}
		predicates[marker] = predicate
	}
	if len(predicates) == 0 {
		predicates = nil
	}
	if err := validateProfileStrings("reusable_patterns", d.ReusablePatterns, func(value string) bool { return value != "" }); err != nil {
		return Profile{}, err
	}
	for i, pattern := range d.ReusablePatterns {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return Profile{}, fmt.Errorf("reusable_patterns[%d]: invalid regex: %w", i, err)
		}
		if compiled.NumSubexp() != 1 {
			return Profile{}, fmt.Errorf("reusable_patterns[%d]: must contain exactly one capture group", i)
		}
	}
	return Profile{
		Name: d.Name, Description: d.Description, IncludeExample: d.IncludeExample,
		ContentExtensions: d.ContentExtensions, RootMarkers: d.RootMarkers,
		markerPredicates: predicates, ReusablePatterns: d.ReusablePatterns,
		ReusableExtensions: d.ReusableExtensions, Resolver: d.Resolver, ImportMap: d.ImportMap,
		ParserCapabilities: ParserCapabilities(c),
	}, nil
}

func validateProfileStrings(field string, values []string, valid func(string) bool) error {
	seen := make(map[string]bool, len(values))
	for i, value := range values {
		if !valid(value) {
			return fmt.Errorf("%s[%d]: invalid value %q", field, i, value)
		}
		if seen[value] {
			return fmt.Errorf("%s[%d]: duplicate value %q", field, i, value)
		}
		seen[value] = true
	}
	return nil
}
