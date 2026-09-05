// Package config gives the pluggable platform and bump source implementations a
// namespaced, read-only view of taggr's configuration, without tying them to the
// configuration library the command line happens to use.
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// EnvPrefix is the prefix of every environment variable taggr reads.
const EnvPrefix = "TAGGR"

// EnvKeyReplacer maps a configuration key to the environment variable spelling of
// its parts: dots and dashes both become underscores, so the key
// "sources.pr-labels.default_bump" is read from TAGGR_SOURCES_PR_LABELS_DEFAULT_BUMP.
func EnvKeyReplacer() *strings.Replacer {
	return strings.NewReplacer(".", "_", "-", "_")
}

// EnvName returns the environment variable a fully qualified key is read from.
func EnvName(key string) string {
	return EnvPrefix + "_" + strings.ToUpper(EnvKeyReplacer().Replace(key))
}

// Lookup resolves a fully qualified configuration key and returns nil when the
// key is not configured.
type Lookup func(key string) any

// Settings is the configuration of a single platform or bump source. Names are
// resolved relative to the namespace the settings were created for, so the Azure
// DevOps platform asking for "org_url" reads "platforms.azuredevops.org_url".
//
// The zero Settings is usable and reports every value as unset, which keeps
// implementations testable without a configuration file.
type Settings struct {
	namespace string
	lookup    Lookup
}

// New returns the settings of the given namespace, backed by lookup.
func New(namespace string, lookup Lookup) Settings {
	return Settings{namespace: namespace, lookup: lookup}
}

// Namespace returns the configuration section these settings read from.
func (s Settings) Namespace() string { return s.namespace }

// Key returns the fully qualified configuration key of name.
func (s Settings) Key(name string) string {
	if s.namespace == "" {
		return name
	}
	return s.namespace + "." + name
}

// EnvName returns the environment variable name is also read from.
func (s Settings) EnvName(name string) string { return EnvName(s.Key(name)) }

// Get returns the raw value of name, or nil when it is not configured.
func (s Settings) Get(name string) any {
	if s.lookup == nil {
		return nil
	}
	return s.lookup(s.Key(name))
}

// String returns name as a trimmed string, or "" when it is not configured.
func (s Settings) String(name string) string {
	return strings.TrimSpace(toString(s.Get(name)))
}

// StringDefault returns name as a string, falling back to def when it is unset.
func (s Settings) StringDefault(name, def string) string {
	if v := s.String(name); v != "" {
		return v
	}
	return def
}

// Require returns name as a string and fails when it is not configured. The error
// names both the configuration key and the environment variable that can set it.
func (s Settings) Require(name string) (string, error) {
	if v := s.String(name); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("missing required setting %q (set it in the config file or as %s)", s.Key(name), s.EnvName(name))
}

// Bool returns name as a boolean, falling back to def when it is unset or is not
// a recognisable boolean.
func (s Settings) Bool(name string, def bool) bool {
	switch v := s.Get(name).(type) {
	case nil:
		return def
	case bool:
		return v
	default:
		b, err := strconv.ParseBool(strings.TrimSpace(toString(v)))
		if err != nil {
			return def
		}
		return b
	}
}

// StringSlice returns name as a list of trimmed, non-empty strings. A plain
// string is split on commas, so a list can also be given in one environment
// variable.
func (s Settings) StringSlice(name string) []string {
	var raw []string
	switch v := s.Get(name).(type) {
	case nil:
		return nil
	case []string:
		raw = v
	case []any:
		for _, item := range v {
			raw = append(raw, toString(item))
		}
	default:
		raw = strings.Split(toString(v), ",")
	}

	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// toString renders a configuration value as a string. Values arrive untyped from
// YAML, JSON or the environment, so numbers and booleans have to be handled too.
func toString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
