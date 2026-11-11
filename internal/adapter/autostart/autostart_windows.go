// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package autostart

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"

	"github.com/CommitBrief/ui/internal/port"
)

// runKeyPath is where Windows looks for per-user programs to start at login.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// registryItem is the Windows login item: one string value under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run.
type registryItem struct {
	keyPath string
	name    string
	exe     func() (string, error)
}

var _ port.AutoStart = registryItem{}

// New returns the Windows login item.
func New(cfg Config) port.AutoStart {
	return registryItem{keyPath: runKeyPath, name: DisplayName, exe: cfg.executable}
}

// Enabled reports whether the value exists.
func (r registryItem) Enabled(context.Context) (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, r.keyPath, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("autostart: open the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()
	if _, _, err := k.GetStringValue(r.name); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("autostart: read the Run value: %w", err)
	}
	return true, nil
}

// Enable writes the value for the running program.
func (r registryItem) Enable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exe, err := r.exe()
	if err != nil {
		return err
	}
	value, err := windowsRunValue(exe)
	if err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, r.keyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: open the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()
	if err := k.SetStringValue(r.name, value); err != nil {
		return fmt.Errorf("autostart: write the Run value: %w", err)
	}
	return nil
}

// Disable deletes the value; a missing one is fine.
func (r registryItem) Disable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, r.keyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("autostart: open the Run key: %w", err)
	}
	defer func() { _ = k.Close() }()
	if err := k.DeleteValue(r.name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("autostart: delete the Run value: %w", err)
	}
	return nil
}
