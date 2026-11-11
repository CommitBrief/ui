// SPDX-License-Identifier: GPL-3.0-or-later

package settings

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
)

// absCLI is an absolute path on every OS (Windows needs a drive letter).
var absCLI = func() string {
	if runtime.GOOS == "windows" {
		return `C:\opt\bin\commitbrief.exe`
	}
	return "/opt/bin/commitbrief"
}()

func TestSettingsUIDefaultsAreValid(t *testing.T) {
	if err := DefaultUI().Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
}

func TestSettingsUIValidateNamesTheField(t *testing.T) {
	for name, tc := range map[string]struct {
		mut   func(*UI)
		field string
	}{
		"language":             {func(u *UI) { u.Language = "de" }, "language"},
		"relative cli path":    {func(u *UI) { u.CLIPath = "bin/commitbrief" }, "cliPath"},
		"unclean cli path":     {func(u *UI) { u.CLIPath = absCLI + "/../x" }, "cliPath"},
		"nul in cli path":      {func(u *UI) { u.CLIPath = absCLI + "\x00b" }, "cliPath"},
		"long cli path":        {func(u *UI) { u.CLIPath = absCLI + strings.Repeat("a", MaxCLIPathLen) }, "cliPath"},
		"zero workers":         {func(u *UI) { u.Workers = 0 }, "workers"},
		"too many workers":     {func(u *UI) { u.Workers = 5 }, "workers"},
		"negative budget":      {func(u *UI) { u.DailyBudgetUSD = -0.01 }, "dailyBudgetUsd"},
		"nan budget":           {func(u *UI) { u.DailyBudgetUSD = math.NaN() }, "dailyBudgetUsd"},
		"huge budget":          {func(u *UI) { u.DailyBudgetUSD = MaxDailyBudgetUSD + 1 }, "dailyBudgetUsd"},
		"zero retention":       {func(u *UI) { u.LogRetentionDays = 0 }, "logRetentionDays"},
		"too long retention":   {func(u *UI) { u.LogRetentionDays = 366 }, "logRetentionDays"},
		"infinite budget":      {func(u *UI) { u.DailyBudgetUSD = math.Inf(1) }, "dailyBudgetUsd"},
		"empty language":       {func(u *UI) { u.Language = "" }, "language"},
		"uppercase language":   {func(u *UI) { u.Language = "EN" }, "language"},
		"language with spaces": {func(u *UI) { u.Language = " en" }, "language"},
	} {
		t.Run(name, func(t *testing.T) {
			u := DefaultUI()
			tc.mut(&u)
			err := u.Validate()
			var se *Error
			if !errors.As(err, &se) || se.Code != CodeValueInvalid || se.Field != tc.field {
				t.Fatalf("Validate() = %v, want %s on %q", err, CodeValueInvalid, tc.field)
			}
		})
	}
}

func TestSettingsUIAcceptsTheBounds(t *testing.T) {
	u := UI{Language: LanguageTR, CLIPath: absCLI, Workers: MaxWorkers, DailyBudgetUSD: MaxDailyBudgetUSD, LogRetentionDays: MaxLogRetentionDays}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	u = UI{Language: LanguageEN, Workers: MinWorkers, LogRetentionDays: MinLogRetentionDays}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsUIStoredRoundTrip(t *testing.T) {
	in := UI{Language: LanguageTR, CLIPath: absCLI, Workers: 3, DailyBudgetUSD: 2.5, LogRetentionDays: 14}
	set, unset := in.ToStored()
	if len(unset) != 0 {
		t.Errorf("unset = %v, want none", unset)
	}
	if got := FromStored(set); got != in {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}

	in.CLIPath = ""
	set, unset = in.ToStored()
	if _, ok := set[KeyCLIPath]; ok || len(unset) != 1 || unset[0] != KeyCLIPath {
		t.Errorf("an empty CLI path must be unset, got set=%v unset=%v", set, unset)
	}
}

func TestSettingsFromStoredFallsBackPerField(t *testing.T) {
	got := FromStored(map[string]string{
		KeyLanguage:         "klingon",
		KeyCLIPath:          "relative/path",
		KeyWorkers:          "9",
		KeyDailyBudgetUSD:   "-4",
		KeyLogRetentionDays: "abc",
	})
	if got != DefaultUI() {
		t.Errorf("damaged rows = %+v, want the defaults %+v", got, DefaultUI())
	}

	// One good row survives next to a bad one.
	got = FromStored(map[string]string{KeyWorkers: "2", KeyLanguage: "??"})
	want := DefaultUI()
	want.Workers = 2
	if got != want {
		t.Errorf("= %+v, want %+v", got, want)
	}
	if FromStored(nil) != DefaultUI() {
		t.Error("no rows must give the defaults")
	}
}

func TestSettingsErrorTextCarriesNoCause(t *testing.T) {
	e := Wrap(CodeValueRejected, "output.lang", errors.New("open /home/someone/.commitbrief/config.yml: denied"))
	if got := e.Error(); got != "settings_value_rejected (output.lang)" {
		t.Errorf("Error() = %q", got)
	}
	if !errors.Is(e, New(CodeValueRejected, "")) || errors.Is(e, New(CodeKeyUnknown, "")) {
		t.Error("errors.Is must compare codes")
	}
	if e.Unwrap() == nil {
		t.Error("the cause must stay reachable for logs")
	}
}

// windowsCLIPathCases drives the Windows rules on any host through the
// ...For variants. Only a plain drive path is accepted; every network, device
// or namespace form is refused from the string alone.
var windowsCLIPathCases = []struct {
	name string
	path string
	ok   bool
}{
	{"empty is unset", ``, true},
	{"drive backslash", `C:\opt\bin\commitbrief.exe`, true},
	{"drive forward slash", `c:/opt/bin/commitbrief.exe`, true},
	{"unc", `\\server\share\commitbrief.exe`, false},
	{"unc forward slashes", `//server/share/commitbrief.exe`, false},
	{"extended unc", `\\?\UNC\server\share\commitbrief.exe`, false},
	{"extended drive", `\\?\C:\opt\commitbrief.exe`, false},
	{"nt namespace unc", `\??\UNC\server\share\commitbrief.exe`, false},
	{"nt namespace drive", `\??\C:\opt\commitbrief.exe`, false},
	{"device", `\\.\pipe\commitbrief`, false},
	{"device volume", `\\.\C:\commitbrief.exe`, false},
	{"mixed separators unc", `/\server\share\commitbrief.exe`, false},
	{"drive relative", `C:commitbrief.exe`, false},
	{"rooted without drive", `\opt\commitbrief.exe`, false},
	{"relative", `bin\commitbrief.exe`, false},
	{"alternate data stream", `C:\opt\commitbrief.exe:stream`, false},
	{"reserved device name", `C:\opt\NUL`, false},
	{"nul byte", "C:\\opt\\a\x00b", false},
}

func TestSettingsValidateCLIPathWindows(t *testing.T) {
	for _, tc := range windowsCLIPathCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCLIPathFor("windows", tc.path)
			if tc.ok && err != nil {
				t.Fatalf("ValidateCLIPathFor(windows, %q) = %v, want nil", tc.path, err)
			}
			if !tc.ok {
				var se *Error
				if !errors.As(err, &se) || se.Code != CodeValueInvalid || se.Field != "cliPath" {
					t.Fatalf("ValidateCLIPathFor(windows, %q) = %v, want cliPath value_invalid", tc.path, err)
				}
			}
		})
	}
}

func TestSettingsFromStoredWindowsDropsNetworkPaths(t *testing.T) {
	for _, tc := range windowsCLIPathCases {
		if tc.path == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			got := fromStoredFor("windows", map[string]string{KeyCLIPath: tc.path}).CLIPath
			want := ""
			if tc.ok {
				want = tc.path
			}
			if got != want {
				t.Errorf("fromStoredFor(windows, %q).CLIPath = %q, want %q", tc.path, got, want)
			}
		})
	}
}

func TestSettingsValidateCLIPathPosixUnchanged(t *testing.T) {
	for path, ok := range map[string]bool{
		"/opt/bin/commitbrief":       true,
		"opt/commitbrief":            false,
		`\\server\share\commitbrief`: false,
		"/opt/a\x00b":                false,
	} {
		err := ValidateCLIPathFor("linux", path)
		if (err == nil) != ok {
			t.Errorf("ValidateCLIPathFor(linux, %q) = %v, want ok=%v", path, err, ok)
		}
	}
}
