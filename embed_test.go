// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"io/fs"
	"testing"
)

func TestAssetsContainFrontendEntryPoint(t *testing.T) {
	info, err := fs.Stat(Assets, "frontend/dist/index.html")
	if err != nil {
		t.Fatalf("embedded frontend build is missing index.html (run `make frontend-build`): %v", err)
	}
	if info.IsDir() {
		t.Fatal("frontend/dist/index.html is a directory")
	}
}
