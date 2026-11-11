// SPDX-License-Identifier: GPL-3.0-or-later

// Command fakecli stands in for the `commitbrief` binary in clicmd's tests. The
// tests build it once and point the adapter at it. Its behaviour comes from
// FAKECLI_* environment variables, so one binary can play every scenario:
//
//	FAKECLI_VERSION       text printed for --version
//	FAKECLI_ARGV_FILE     file that receives a JSON record of argv, env and stdin
//	FAKECLI_STDOUT_FILE   file whose content is copied to stdout
//	FAKECLI_BIG_STDOUT    write this many bytes to stdout
//	FAKECLI_STDERR        text written to stderr
//	FAKECLI_STDERR_LINES  write this many numbered lines to stderr, slowly
//	FAKECLI_EXIT          exit code
//	FAKECLI_SLEEP_MS      sleep this long before exiting
//	FAKECLI_IGNORE_TERM   ignore SIGTERM (exercises the SIGKILL escalation)
//	FAKECLI_SPAWN_GRANDCHILD  spawn a long-lived grandchild that shares stdout/stderr
//	FAKECLI_DETACH_GRANDCHILD  the grandchild gets the null device instead of our stdout/stderr
//	FAKECLI_PIDFILE       where the grandchild appends its pid; the CLI waits for it
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if os.Getenv("FAKECLI_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if os.Getenv("FAKECLI_ROLE") == "sleeper" {
		if f := os.Getenv("FAKECLI_PIDFILE"); f != "" {
			fh, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				fmt.Fprintln(fh, os.Getpid())
				fh.Close()
			}
		}
		time.Sleep(60 * time.Second)
		return
	}

	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		v := os.Getenv("FAKECLI_VERSION")
		if v == "" {
			v = "commitbrief 1.21.2 (commit abc1234, built 2026-10-31T00:00:00Z)"
		}
		fmt.Println(v)
		return
	}

	stdin, _ := io.ReadAll(os.Stdin)
	cwd, _ := os.Getwd()
	if f := os.Getenv("FAKECLI_ARGV_FILE"); f != "" {
		rec := map[string]any{
			"args":        args,
			"cwd":         cwd,
			"stdin_bytes": len(stdin),
			"stdin":       string(stdin),
			"env": map[string]string{
				"NO_COLOR":            os.Getenv("NO_COLOR"),
				"PATH":                os.Getenv("PATH"),
				"GIT_TERMINAL_PROMPT": os.Getenv("GIT_TERMINAL_PROMPT"),
			},
		}
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(f, b, 0o600)
	}

	if os.Getenv("FAKECLI_SPAWN_GRANDCHILD") == "1" {
		gc := exec.Command(os.Args[0])
		gc.Env = append(os.Environ(), "FAKECLI_ROLE=sleeper")
		if os.Getenv("FAKECLI_DETACH_GRANDCHILD") != "1" {
			gc.Stdout = os.Stdout
			gc.Stderr = os.Stderr
		}
		if err := gc.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "fakecli: spawn grandchild:", err)
			os.Exit(3)
		}
		// Let the grandchild register itself before this process can exit, so a
		// test never races the registration.
		if f := os.Getenv("FAKECLI_PIDFILE"); f != "" {
			for i := 0; i < 250; i++ {
				if b, err := os.ReadFile(f); err == nil && len(b) > 0 {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}

	if f := os.Getenv("FAKECLI_STDOUT_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakecli:", err)
			os.Exit(3)
		}
		os.Stdout.Write(b)
	}
	if n, _ := strconv.Atoi(os.Getenv("FAKECLI_BIG_STDOUT")); n > 0 {
		chunk := make([]byte, 64<<10)
		for i := range chunk {
			chunk[i] = 'x'
		}
		for n > 0 {
			w := len(chunk)
			if n < w {
				w = n
			}
			if _, err := os.Stdout.Write(chunk[:w]); err != nil {
				break
			}
			n -= w
		}
	}
	if s := os.Getenv("FAKECLI_STDERR"); s != "" {
		fmt.Fprintln(os.Stderr, s)
	}
	if n, _ := strconv.Atoi(os.Getenv("FAKECLI_STDERR_LINES")); n > 0 {
		for i := 1; i <= n; i++ {
			fmt.Fprintf(os.Stderr, "progress line %d\n", i)
			time.Sleep(10 * time.Millisecond)
		}
	}
	if ms, _ := strconv.Atoi(os.Getenv("FAKECLI_SLEEP_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if c, _ := strconv.Atoi(os.Getenv("FAKECLI_EXIT")); c != 0 {
		os.Exit(c)
	}
}
