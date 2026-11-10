// SPDX-License-Identifier: GPL-3.0-or-later

// Package ui exposes the embedded frontend build so that the composition root
// in cmd/commitbrief-ui can hand it to the desktop adapter. go:embed cannot
// reach outside its own directory, so the embed directive has to live next to
// the frontend directory, at the module root.
package ui

import "embed"

// Assets holds the production frontend build (frontend/dist). Run
// `make frontend-build` before compiling if the directory is missing.
//
//go:embed all:frontend/dist
var Assets embed.FS
