// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package autostart

import (
	"context"

	"github.com/CommitBrief/ui/internal/port"
)

type unsupported struct{}

// New returns a login item that reports every operation as unsupported: this
// operating system has no login item the UI manages.
func New(Config) port.AutoStart { return unsupported{} }

func (unsupported) Enabled(context.Context) (bool, error) { return false, port.ErrAutostartUnsupported }
func (unsupported) Enable(context.Context) error          { return port.ErrAutostartUnsupported }
func (unsupported) Disable(context.Context) error         { return port.ErrAutostartUnsupported }
