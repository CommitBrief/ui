// SPDX-License-Identifier: GPL-3.0-or-later

// Command commitbrief-ui is the composition root of CommitBrief UI. It wires
// the adapters to the application core and starts the desktop shell.
package main

import (
	"log"

	ui "github.com/CommitBrief/ui"
	"github.com/CommitBrief/ui/internal/adapter/desktop"
)

func main() {
	err := desktop.Run(desktop.Options{
		Assets: ui.Assets,
	})
	if err != nil {
		log.Fatal(err)
	}
}
