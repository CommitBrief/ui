// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package clicmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/ui/internal/domain/errcode"
	"github.com/CommitBrief/ui/internal/domain/settings"
	"github.com/CommitBrief/ui/internal/port"
)

// These tests run the real `commitbrief` binary that is installed on this
// machine; they are not part of CI (`go test -tags integration ./internal/adapter/clicmd`).
// That binary is the user's own tool, and the tests never let it near the
// user's real configuration: every call runs with HOME (and USERPROFILE) set
// to a fresh temporary directory and COMMITBRIEF_CONFIG emptied, so the CLI
// reads and writes ~/.commitbrief/config.yml of that directory only. The
// provider keys and URLs in the real environment are left alone, so a result
// can still be influenced by them; the assertions below do not depend on that.

// isolatedEngine returns an Engine on the real binary with a throwaway home.
func isolatedEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	home := t.TempDir()
	env := testEnv{
		vars: map[string]string{
			"HOME":               home,
			"USERPROFILE":        home,
			"COMMITBRIEF_CONFIG": "",
			"XDG_CONFIG_HOME":    filepath.Join(home, ".config"),
		},
		home: home,
	}
	eng := New(Config{
		Env:     env,
		DataDir: filepath.Join(home, "ui-data"),
		// No login-shell probe: the process PATH and the known install locations
		// are enough to find an installed binary, and the probe would run the
		// user's shell start-up files.
		Probe: func(context.Context) (string, error) { return "", nil },
	})
	if _, err := eng.Version(context.Background()); err != nil {
		if errcode.Is(err, errcode.CLINotFound) {
			t.Skipf("no commitbrief binary on this machine: %v", err)
		}
		t.Fatal(err)
	}
	return eng, home
}

func TestIntegrationSetConfigThenGetConfig(t *testing.T) {
	ctx := context.Background()
	eng, home := isolatedEngine(t)

	if err := eng.SetConfig(ctx, "output.lang", "tr", port.UserScope()); err != nil {
		t.Fatalf("SetConfig(output.lang, tr): %v", err)
	}
	got, err := eng.ConfigGet(ctx, "output.lang")
	if err != nil {
		t.Fatalf("ConfigGet(output.lang): %v", err)
	}
	if got != "tr" {
		t.Errorf("ConfigGet(output.lang) = %q, want %q", got, "tr")
	}

	// The write landed in the throwaway home, through the CLI.
	file := filepath.Join(home, ".commitbrief", "config.yml")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the CLI did not write %s: %v", file, err)
	}
	if !strings.Contains(string(b), "lang: tr") {
		t.Errorf("config file lacks the new value:\n%s", b)
	}

	// Other scalar kinds round-trip too.
	for key, want := range map[string]string{"cache.ttl_days": "21", "output.stream": "false", "output.color": "never", "cost.warn_threshold_usd": "1.25"} {
		if err := eng.SetConfig(ctx, key, want, port.UserScope()); err != nil {
			t.Fatalf("SetConfig(%s): %v", key, err)
		}
		if got, err := eng.ConfigGet(ctx, key); err != nil || got != want {
			t.Errorf("ConfigGet(%s) = %q, %v; want %q", key, got, err, want)
		}
	}
}

// Every key of the Settings catalogue must be one the real CLI can read: this is
// what keeps the catalogue honest when the CLI moves.
func TestIntegrationEveryCatalogKeyIsReadable(t *testing.T) {
	ctx := context.Background()
	eng, _ := isolatedEngine(t)
	for _, f := range settings.GlobalKeys() {
		if _, err := eng.ConfigGet(ctx, f.Key); err != nil {
			t.Errorf("ConfigGet(%s): %v", f.Key, err)
		}
	}
	for _, p := range []string{"anthropic", "openai", "ollama"} {
		for _, field := range []string{"model", "base_url"} {
			key := settings.ProviderKey(p, field)
			if _, err := eng.ConfigGet(ctx, key); err != nil {
				t.Errorf("ConfigGet(%s): %v", key, err)
			}
		}
	}
}

func TestIntegrationProvidersAndUse(t *testing.T) {
	ctx := context.Background()
	eng, _ := isolatedEngine(t)

	before, err := eng.Providers(ctx)
	if err != nil || len(before) == 0 {
		t.Fatalf("Providers() = %v, %v", before, err)
	}
	if err := eng.UseProvider(ctx, "openai", port.UserScope()); err != nil {
		t.Fatalf("UseProvider(openai): %v", err)
	}
	after, err := eng.Providers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var active []string
	for _, p := range after {
		if p.Active {
			active = append(active, p.Name)
		}
	}
	if len(active) != 1 || active[0] != "openai" {
		t.Errorf("active providers = %v, want [openai]", active)
	}
}

// A secret is written on stdin when the CLI has it (v1.22.0) and refused
// otherwise; it never appears on the command line either way.
func TestIntegrationSecretWriteMatchesTheCLICapability(t *testing.T) {
	ctx := context.Background()
	eng, home := isolatedEngine(t)
	const secret = "sk-integration-test-not-a-real-key-0123456789"

	caps, err := eng.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = eng.SetConfig(ctx, "providers.openai.api_key", secret, port.UserScope())
	file := filepath.Join(home, ".commitbrief", "config.yml")

	if !caps.ConfigSetStdin {
		if !errors.Is(err, errcode.ErrCapabilityMissing) {
			t.Fatalf("commitbrief %s lacks config_set_stdin: err = %v, want capability_missing", caps.Version, err)
		}
		t.Logf("commitbrief %s has no config_set_stdin: capability_missing, as expected before v1.22.0", caps.Version)
		if b, rerr := os.ReadFile(file); rerr == nil && strings.Contains(string(b), secret) {
			t.Error("the secret reached the config file through some other path")
		}
		return
	}
	if err != nil {
		t.Fatalf("SetConfig(api_key) on commitbrief %s: %v", caps.Version, err)
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), secret) {
		t.Errorf("the key was not written to %s (err = %v)", file, err)
	}
	if _, err := eng.ConfigGet(ctx, "providers.openai.api_key"); err == nil {
		t.Error("ConfigGet must refuse an API key")
	}
}

func TestIntegrationUpgradeCheckReportsAMethod(t *testing.T) {
	ctx := context.Background()
	eng, _ := isolatedEngine(t)
	rep, err := eng.CheckUpgrade(ctx)
	if err != nil {
		// The check reaches the GitHub release API; an offline machine or a
		// rate limit is not a failure of the adapter.
		t.Skipf("upgrade check unavailable: %v", err)
	}
	if rep.Current == "" || rep.Method == "" {
		t.Errorf("report = %+v", rep)
	}
}
