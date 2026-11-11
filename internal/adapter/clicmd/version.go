// SPDX-License-Identifier: GPL-3.0-or-later

package clicmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/CommitBrief/ui/internal/port"
)

// Capability is a UI feature gated on a minimum CLI version (UI-0003 section 3).
// Capabilities are never discovered through the hidden --gen-surface flag, which
// is not a stable contract (analysis K17).
type Capability string

const (
	// CapBase: local review `--json`, `remote pr --no-post --json`, `config`,
	// `providers`, `upgrade --json`.
	CapBase Capability = "base"
	// CapPosting: the machine-readable posting report and the verdict ceiling
	// (CLI ADR-0048, shipped by CLI v1.22.0).
	CapPosting Capability = "posting"
	// CapConfigSetStdin: `commitbrief config set <key> -` reads the value from
	// stdin (CLI phase 11b, target v1.22.0). Secrets are written only this way.
	CapConfigSetStdin Capability = "config_set_stdin"
)

type minVersion struct{ major, minor, patch int }

func (m minVersion) String() string { return fmt.Sprintf("%d.%d.%d", m.major, m.minor, m.patch) }

// capabilityTable is the capability -> minimum CLI table. Moving a row is a
// deliberate change: it decides which buttons the UI disables.
var capabilityTable = map[Capability]minVersion{
	CapBase:    {1, 21, 2},
	CapPosting: {1, 22, 0},
	// Planned for v1.22.0; verify the number when the release is cut.
	CapConfigSetStdin: {1, 22, 0},
}

// MinVersion returns the minimum CLI version for c.
func MinVersion(c Capability) string { return capabilityTable[c].String() }

// Satisfies reports whether v supports c.
//
// A source build ("dev") carries no version number, so it cannot prove any
// feature: it satisfies the base capability only, so developers can run the UI
// against their own build. For every newer capability a dev build is treated as
// lacking it. The cost of a wrong "yes" is not symmetric: a dev build without
// config_set_stdin would read `config set <key> -` literally and overwrite an
// API key with "-". A developer who needs the feature installs a release.
func Satisfies(v port.CLIVersion, c Capability) bool {
	m, ok := capabilityTable[c]
	if !ok {
		return false
	}
	if v.Dev {
		return c == CapBase
	}
	return v.AtLeast(m.major, m.minor, m.patch)
}

var (
	// `commitbrief 1.21.2 (commit abc1234, built 2026-10-31T10:00:00Z)`
	versionLineRe = regexp.MustCompile(`^commitbrief\s+(\S+)(?:\s+\(commit\s+([^,)]*),\s*built\s+([^)]*)\))?\s*$`)
	semverRe      = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.\-]+))?(?:\+[0-9A-Za-z.\-]+)?$`)
)

// ParseVersionOutput parses the output of `commitbrief --version` (analysis
// K16). Login shells and wrappers may print noise, so the first line that looks
// like the version line wins. A non-semver token such as "dev" is a source
// build and yields a Dev version.
func ParseVersionOutput(out string) (port.CLIVersion, error) {
	for _, line := range strings.Split(out, "\n") {
		m := versionLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		v := port.CLIVersion{Raw: m[1], Commit: m[2], Date: strings.TrimSpace(m[3])}
		s := semverRe.FindStringSubmatch(m[1])
		if s == nil {
			v.Dev = true
			return v, nil
		}
		v.Major, _ = strconv.Atoi(s[1])
		v.Minor, _ = strconv.Atoi(s[2])
		v.Patch, _ = strconv.Atoi(s[3])
		v.Pre = s[4]
		return v, nil
	}
	return port.CLIVersion{}, fmt.Errorf("no commitbrief version line in output")
}
