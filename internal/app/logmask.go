// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"regexp"
	"sort"
	"strings"

	"github.com/CommitBrief/ui/internal/port"
)

// maskText replaces a secret. It is the same everywhere so a log reader can
// tell "something was here" without learning what.
const maskText = "***"

// minLiteralLen is the shortest environment value masked verbatim: shorter
// values ("1", "true", "prod") would blank out ordinary words.
const minLiteralLen = 8

// secretPatterns find credentials by shape. Each is a regexp and the
// replacement that keeps the surrounding context (the key name, the scheme).
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Authorization headers: keep the scheme, drop the credential.
	{regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic|token)\s+)[^\s"']+`), "${1}" + maskText},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + maskText},
	// Well-known token shapes.
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), maskText},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), maskText},
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`), maskText},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`), maskText},
	{regexp.MustCompile(`\bxai-[A-Za-z0-9]{20,}`), maskText},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`), maskText},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), maskText},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), maskText},
	// JSON members: "api_key": "value".
	{regexp.MustCompile(`(?i)("[A-Za-z0-9_.-]*(?:api[_-]?key|secret|password|passwd|token)"\s*:\s*)"[^"]*"`), `${1}"` + maskText + `"`},
	// key=value and key: value, where the key says it is a credential. The
	// key must end at the credential word (so "input_tokens: 4231" and
	// "max_tokens=1024" stay readable) and the value must look like one (6+
	// characters).
	{regexp.MustCompile(`(?i)(\b[A-Za-z0-9_.-]*(?:api[_-]?key|secret|password|passwd|token)\b\s*[=:]\s*)(?:"[^"]{6,}"|'[^']{6,}'|[^\s"',;]{6,})`), "${1}" + maskText},
}

// Masker removes credentials from text before it is written to a log or sent
// to the screen (architecture "Güvenlik": GH_TOKEN and API key patterns are
// masked in logs). It works by shape, and additionally by value for the
// secrets that sit in this process's own environment.
type Masker struct {
	literals []string
}

// NewMasker builds a Masker. env may be nil; when set, every variable whose
// name says it holds a credential contributes its value (8+ characters) to the
// list of strings masked verbatim.
func NewMasker(env port.ProcessEnv) *Masker {
	m := &Masker{}
	if env == nil {
		return m
	}
	seen := map[string]bool{}
	for _, kv := range env.Environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || len(value) < minLiteralLen || seen[value] || !secretName(name) {
			continue
		}
		seen[value] = true
		m.literals = append(m.literals, value)
	}
	// Longest first, so a value that contains another is masked whole.
	sort.Slice(m.literals, func(i, j int) bool { return len(m.literals[i]) > len(m.literals[j]) })
	return m
}

func secretName(name string) bool {
	n := strings.ToUpper(name)
	for _, w := range []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// Mask returns s with every recognised credential replaced by "***". It is
// idempotent and safe for concurrent use.
func (m *Masker) Mask(s string) string {
	if s == "" {
		return s
	}
	for _, lit := range m.literals {
		s = strings.ReplaceAll(s, lit, maskText)
	}
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
