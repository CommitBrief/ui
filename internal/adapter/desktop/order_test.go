// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"os"
	"strings"
	"testing"
)

// Run cannot be executed in a test (it needs a display), so the ordering that
// protects the running instance's database is pinned at the source level:
// application.New takes the Wails single-instance guard (a second launch wakes
// the first instance and exits inside it), and OpenState, which opens, migrates
// or restores the database, must come after it.
func TestRunOpensStateAfterSingleInstanceGuard(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	// Only look inside Run.
	body := text[strings.Index(text, "func Run("):]

	guard := strings.Index(body, "application.New(")
	open := strings.Index(body, "opts.OpenState()")
	register := strings.Index(body, "app.RegisterService(")
	if guard < 0 || open < 0 || register < 0 {
		t.Fatalf("Run no longer contains application.New / opts.OpenState / RegisterService (%d, %d, %d); update this guard test", guard, open, register)
	}
	if guard >= open || open >= register {
		t.Errorf("order in Run must be application.New (single-instance guard) -> opts.OpenState -> RegisterService; got positions %d, %d, %d", guard, open, register)
	}
	if strings.Contains(text[:strings.Index(text, "func Run(")], "opts.OpenState()") {
		t.Error("OpenState is called before Run")
	}
}
