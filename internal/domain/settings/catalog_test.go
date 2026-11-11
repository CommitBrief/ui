// SPDX-License-Identifier: GPL-3.0-or-later

package settings

import (
	"errors"
	"strings"
	"testing"
)

func TestSettingsCatalogLookup(t *testing.T) {
	for key, want := range map[string]struct {
		kind     Kind
		fileOnly bool
		secret   bool
	}{
		"provider":                          {KindString, false, false},
		"output.color":                      {KindEnum, false, false},
		"cache.ttl_days":                    {KindInt, false, false},
		"cost.warn_threshold_usd":           {KindFloat, false, false},
		"review.sandbox_command":            {KindString, true, false},
		"guard.secret_patterns":             {KindString, true, false},
		"providers.openai.api_key":          {KindString, false, true},
		"providers.openai.model":            {KindString, false, false},
		"providers.ollama.base_url":         {KindString, false, false},
		"providers.openai-compatible.model": {KindString, false, false},
	} {
		f, ok := LookupConfigKey(key)
		if !ok {
			t.Errorf("%s: not found", key)
			continue
		}
		if f.Kind != want.kind || f.FileOnly != want.fileOnly || f.Secret != want.secret {
			t.Errorf("%s = %+v", key, f)
		}
	}
}

func TestSettingsCatalogRefusesWhatTheCLIDoesNotSet(t *testing.T) {
	for _, key := range []string{
		"version", "commit.type", "output", "output.nope", "providers", "providers.openai",
		"providers.openai.pricing", "providers.openai.context_window", "providers.openai.api_key_env",
		"providers..model", "providers.a.b.model", "providers.<provider>.model", "", "Provider",
		"providers.x y.model",
	} {
		if f, ok := LookupConfigKey(key); ok {
			t.Errorf("%q must not resolve, got %+v", key, f)
		}
	}
}

func TestSettingsCatalogGlobalKeysExcludeProviderAndSecretKeys(t *testing.T) {
	keys := GlobalKeys()
	if len(keys) == 0 {
		t.Fatal("no global keys")
	}
	seen := map[string]bool{}
	for _, f := range keys {
		if f.PerProvider || f.Secret {
			t.Errorf("%s must not be a global key", f.Key)
		}
		if seen[f.Key] {
			t.Errorf("%s listed twice", f.Key)
		}
		seen[f.Key] = true
	}
	for _, want := range []string{"provider", "output.lang", "review.timeout", "review.sandbox_command", "guard.secret_patterns"} {
		if !seen[want] {
			t.Errorf("%s missing from the global keys", want)
		}
	}
}

func TestSettingsCatalogValidateValue(t *testing.T) {
	type tc struct {
		key, in, out string
		bad          bool
	}
	for _, c := range []tc{
		{key: "output.stream", in: "TRUE", out: "true"},
		{key: "output.stream", in: " off ", out: "false"},
		{key: "output.stream", in: "maybe", bad: true},
		{key: "output.stream", in: "", bad: true},
		{key: "output.color", in: "never", out: "never"},
		{key: "output.color", in: "Never", bad: true},
		{key: "cache.ttl_days", in: " 14 ", out: "14"},
		{key: "cache.ttl_days", in: "-1", bad: true},
		{key: "cache.ttl_days", in: "1.5", bad: true},
		{key: "cache.ttl_days", in: "99999999999999999999", bad: true},
		{key: "cost.warn_threshold_usd", in: "0.50", out: "0.5"},
		{key: "cost.warn_threshold_usd", in: "-0.1", bad: true},
		{key: "cost.warn_threshold_usd", in: "NaN", bad: true},
		{key: "cost.warn_threshold_usd", in: "Inf", bad: true},
		{key: "review.timeout", in: "", out: ""},
		{key: "review.timeout", in: "0", out: "0"},
		{key: "review.timeout", in: "600", out: "600"},
		{key: "review.timeout", in: "1h30m", out: "1h30m"},
		{key: "review.timeout", in: "soon", bad: true},
		{key: "review.timeout", in: "-5m", bad: true},
		{key: "output.lang", in: "tr", out: "tr"},
		{key: "output.lang", in: "", out: ""},
		{key: "command.default", in: "--unstaged --cli gemini", out: "--unstaged --cli gemini"},
		{key: "command.default", in: "a\x00b", bad: true},
		{key: "providers.openai.model", in: "gpt-5.4-mini", out: "gpt-5.4-mini"},
		{key: "providers.openai.api_key", in: "sk-secret", out: "sk-secret"},
		{key: "providers.openai.api_key", in: "", bad: true},
		{key: "providers.openai.api_key", in: "line one\nline two", bad: true},
		{key: "providers.openai.api_key", in: "key\r", bad: true},
	} {
		f, ok := LookupConfigKey(c.key)
		if !ok {
			t.Fatalf("%s: not in the catalogue", c.key)
		}
		got, err := f.ValidateValue(c.key, c.in)
		if c.bad {
			var se *Error
			if !errors.As(err, &se) || se.Code != CodeValueInvalid || se.Field != c.key {
				t.Errorf("%s=%q: err = %v, want %s on the key", c.key, c.in, err, CodeValueInvalid)
			}
			continue
		}
		if err != nil || got != c.out {
			t.Errorf("%s=%q: got %q, %v; want %q", c.key, c.in, got, err, c.out)
		}
	}
}

func TestSettingsCatalogErrorsNeverEchoTheValue(t *testing.T) {
	f, _ := LookupConfigKey("providers.openai.api_key")
	_, err := f.ValidateValue("providers.openai.api_key", "sk-very-secret\nsecond line")
	if err == nil {
		t.Fatal("want an error")
	}
	if s := err.Error(); len(s) == 0 || strings.Contains(s, "sk-very-secret") {
		t.Errorf("error text %q must not carry the value", s)
	}
}

func TestSettingsProviderKeyAndName(t *testing.T) {
	if got := ProviderKey("openai", "model"); got != "providers.openai.model" {
		t.Errorf("ProviderKey = %q", got)
	}
	for name, want := range map[string]bool{
		"openai": true, "claude-cli": true, "openai-compatible": true, "a_b": true,
		"": false, "a.b": false, "-x": false, "a b": false, "a/b": false,
	} {
		if got := ValidProviderName(name); got != want {
			t.Errorf("ValidProviderName(%q) = %v, want %v", name, got, want)
		}
	}
}
