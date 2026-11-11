// SPDX-License-Identifier: GPL-3.0-or-later

package settings

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of a CLI configuration value.
type Kind string

const (
	KindString Kind = "string"
	KindBool   Kind = "bool"
	KindInt    Kind = "int"
	KindFloat  Kind = "float"
	KindEnum   Kind = "enum"
)

// Config groups, in the order the screen shows them.
const (
	GroupProvider = "provider"
	GroupOutput   = "output"
	GroupCache    = "cache"
	GroupGuard    = "guard"
	GroupCost     = "cost"
	GroupCommand  = "command"
	GroupReview   = "review"
)

// ConfigField describes one CLI configuration key (config-schema.md, the keys
// `config set` accepts plus the two list keys that can only be hand-edited).
type ConfigField struct {
	// Key is the dotted path. For a per-provider key it holds the placeholder
	// "<provider>" in place of the provider name.
	Key   string `json:"key"`
	Group string `json:"group"`
	Kind  Kind   `json:"kind"`
	// Enum lists the allowed values of a KindEnum field.
	Enum []string `json:"enum,omitempty"`
	// Min and Max bound a KindInt or KindFloat field; nil means unbounded.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// FileOnly marks a list key: it is shown (read-only) and edited in the
	// config file; `config set` refuses it.
	FileOnly bool `json:"fileOnly"`
	// Secret marks a credential: it is written but never read back.
	Secret bool `json:"secret"`
	// PerProvider marks a providers.<provider>.<field> key.
	PerProvider bool `json:"perProvider"`
}

func ptr(f float64) *float64 { return &f }

// maxStringValue bounds a free-form string value; the adapter bounds the
// argument as well, this keeps the message a field error instead of a refusal.
const maxStringValue = 4096

// catalog is the closed list of keys. `version` is not in it (managed by CLI
// migrations); `commit.*` is not either: `config set` has no path for it.
var catalog = []ConfigField{
	{Key: "provider", Group: GroupProvider, Kind: KindString},
	{Key: "providers.<provider>.api_key", Group: GroupProvider, Kind: KindString, Secret: true, PerProvider: true},
	{Key: "providers.<provider>.model", Group: GroupProvider, Kind: KindString, PerProvider: true},
	{Key: "providers.<provider>.base_url", Group: GroupProvider, Kind: KindString, PerProvider: true},

	{Key: "output.lang", Group: GroupOutput, Kind: KindString},
	{Key: "output.stream", Group: GroupOutput, Kind: KindBool},
	{Key: "output.color", Group: GroupOutput, Kind: KindEnum, Enum: []string{"auto", "always", "never"}},

	{Key: "cache.enabled", Group: GroupCache, Kind: KindBool},
	{Key: "cache.ttl_days", Group: GroupCache, Kind: KindInt, Min: ptr(0)},
	{Key: "cache.max_size_mb", Group: GroupCache, Kind: KindInt, Min: ptr(0)},

	{Key: "guard.secret_scan", Group: GroupGuard, Kind: KindBool},
	{Key: "guard.token_preflight", Group: GroupGuard, Kind: KindBool},
	{Key: "guard.injection_scan", Group: GroupGuard, Kind: KindBool},
	{Key: "guard.secret_patterns", Group: GroupGuard, Kind: KindString, FileOnly: true},

	{Key: "cost.warn_threshold_usd", Group: GroupCost, Kind: KindFloat, Min: ptr(0)},

	{Key: "command.default", Group: GroupCommand, Kind: KindString},

	{Key: "review.flaky", Group: GroupReview, Kind: KindBool},
	{Key: "review.baseline", Group: GroupReview, Kind: KindBool},
	{Key: "review.architecture", Group: GroupReview, Kind: KindBool},
	{Key: "review.architecture_file", Group: GroupReview, Kind: KindString},
	{Key: "review.sandbox_rerun", Group: GroupReview, Kind: KindInt, Min: ptr(0)},
	{Key: "review.sandbox_command", Group: GroupReview, Kind: KindString, FileOnly: true},
	{Key: "review.timeout", Group: GroupReview, Kind: KindString},
}

// Catalog returns every key the UI manages, per-provider templates included.
func Catalog() []ConfigField {
	out := make([]ConfigField, len(catalog))
	copy(out, catalog)
	return out
}

// GlobalKeys returns the catalogue entries that are read as plain keys: the
// per-provider templates are excluded (they are read through the provider
// views) and so is every secret.
func GlobalKeys() []ConfigField {
	var out []ConfigField
	for _, f := range catalog {
		if f.PerProvider || f.Secret {
			continue
		}
		out = append(out, f)
	}
	return out
}

// providerNameRe is the shape of a provider name in a config key.
var providerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ProviderKey returns the config key of a per-provider field, for example
// ProviderKey("openai", "model") = "providers.openai.model".
func ProviderKey(provider, field string) string {
	return "providers." + provider + "." + field
}

// ValidProviderName reports whether name can be a provider name in a config
// key. The dots a name may contain would add path segments, so they are not
// accepted: no built-in provider has one.
func ValidProviderName(name string) bool {
	return providerNameRe.MatchString(name) && !strings.Contains(name, ".")
}

// LookupConfigKey finds the catalogue entry of a concrete key. A key like
// "providers.openai.model" resolves to the "providers.<provider>.model"
// template. ok is false for any key the UI does not manage.
func LookupConfigKey(key string) (ConfigField, bool) {
	parts := strings.Split(key, ".")
	if len(parts) == 3 && parts[0] == "providers" && ValidProviderName(parts[1]) {
		tmpl := "providers.<provider>." + parts[2]
		for _, f := range catalog {
			if f.Key == tmpl {
				return f, true
			}
		}
		return ConfigField{}, false
	}
	for _, f := range catalog {
		if f.Key == key && !f.PerProvider {
			return f, true
		}
	}
	return ConfigField{}, false
}

// ValidateValue checks a value against the field's type and range and returns
// it in the spelling handed to `config set` (a bool as "true"/"false", numbers
// trimmed). A secret value is checked for shape only: one non-empty line. The
// error is an *Error with CodeValueInvalid and Field set to the key.
func (f ConfigField) ValidateValue(key, value string) (string, error) {
	bad := func() (string, error) { return "", New(CodeValueInvalid, key) }
	if strings.ContainsRune(value, 0) || len(value) > maxStringValue {
		return bad()
	}
	switch f.Kind {
	case KindBool:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "yes", "on", "1":
			return "true", nil
		case "false", "no", "off", "0":
			return "false", nil
		}
		return bad()
	case KindInt:
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || !f.inRange(float64(n)) {
			return bad()
		}
		return strconv.FormatInt(n, 10), nil
	case KindFloat:
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || !f.inRange(v) {
			return bad()
		}
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case KindEnum:
		for _, e := range f.Enum {
			if value == e {
				return value, nil
			}
		}
		return bad()
	default:
		if f.Key == "review.timeout" && !validTimeout(value) {
			return bad()
		}
		if f.Secret && (value == "" || strings.ContainsAny(value, "\r\n")) {
			return bad()
		}
		return value, nil
	}
}

// validTimeout accepts what the CLI accepts for review.timeout: empty or "0"
// (no deadline), a whole number of seconds, or a Go duration such as "10m".
func validTimeout(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	if _, err := strconv.ParseUint(v, 10, 31); err == nil {
		return true
	}
	d, err := time.ParseDuration(v)
	return err == nil && d >= 0
}

func (f ConfigField) inRange(v float64) bool {
	if f.Min != nil && v < *f.Min {
		return false
	}
	if f.Max != nil && v > *f.Max {
		return false
	}
	return true
}
