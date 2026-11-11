// SPDX-License-Identifier: GPL-3.0-or-later

// Command fakegh stands in for the `gh` executable in the gh adapter's tests.
// The tests build it once, name it gh and put it on the PATH they hand to the
// adapter. Its behaviour comes from environment variables:
//
//	FAKEGH_ARGV_FILE   file that receives a JSON record of argv and selected env
//	FAKEGH_STDOUT      text written to stdout
//	FAKEGH_STDERR      text written to stderr
//	FAKEGH_EXIT        exit code
//	FAKEGH_SLEEP_MS    sleep this long before printing
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

func main() {
	if f := os.Getenv("FAKEGH_ARGV_FILE"); f != "" {
		rec := map[string]any{
			"args": os.Args[1:],
			"env": map[string]string{
				"PATH":                  os.Getenv("PATH"),
				"NO_COLOR":              os.Getenv("NO_COLOR"),
				"GH_PROMPT_DISABLED":    os.Getenv("GH_PROMPT_DISABLED"),
				"GH_NO_UPDATE_NOTIFIER": os.Getenv("GH_NO_UPDATE_NOTIFIER"),
				"GH_PAGER":              os.Getenv("GH_PAGER"),
				"GH_TOKEN":              os.Getenv("GH_TOKEN"),
			},
		}
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(f, b, 0o600)
	}
	if ms, _ := strconv.Atoi(os.Getenv("FAKEGH_SLEEP_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if s := os.Getenv("FAKEGH_STDOUT"); s != "" {
		fmt.Fprintln(os.Stdout, s)
	}
	if s := os.Getenv("FAKEGH_STDERR"); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	if c, _ := strconv.Atoi(os.Getenv("FAKEGH_EXIT")); c != 0 {
		os.Exit(c)
	}
}
