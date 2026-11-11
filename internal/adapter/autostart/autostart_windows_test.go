// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package autostart

import (
	"context"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// The real Run key is never touched: the item writes under a throwaway key of
// its own that the test deletes.
func TestAutostartWindowsRegistryValue(t *testing.T) {
	keyPath := fmt.Sprintf(`Software\CommitBrief-UI-Test\run-%d`, time.Now().UnixNano())
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\CommitBrief-UI-Test`)
	})
	const exe = `C:\Program Files\CommitBrief UI\commitbrief-ui.exe`
	item := registryItem{keyPath: keyPath, name: DisplayName, exe: func() (string, error) { return exe, nil }}
	ctx := context.Background()

	if on, err := item.Enabled(ctx); err != nil || on {
		t.Fatalf("fresh: %v, %v", on, err)
	}
	if err := item.Disable(ctx); err != nil {
		t.Fatalf("disabling when the key does not exist: %v", err)
	}
	if err := item.Enable(ctx); err != nil {
		t.Fatal(err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := k.GetStringValue(DisplayName)
	_ = k.Close()
	if err != nil || got != `"`+exe+`"` {
		t.Fatalf("value = %q, %v", got, err)
	}
	if on, _ := item.Enabled(ctx); !on {
		t.Error("not enabled")
	}
	if err := item.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if on, _ := item.Enabled(ctx); on {
		t.Error("still enabled")
	}

	bad := registryItem{keyPath: keyPath, name: DisplayName, exe: func() (string, error) { return `\\host\share\x.exe`, nil }}
	if err := bad.Enable(ctx); err == nil {
		t.Error("a network path was written to the Run key")
	}
}
