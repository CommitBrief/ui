// SPDX-License-Identifier: GPL-3.0-or-later

package gitinfo

import "strings"

// originURL returns the value of remote.origin.url from the text of a git
// config file. When the key is set more than once the last value wins, like
// `git config --get`. It understands what real configs contain: section
// headers [remote "origin"] and the legacy [remote.origin], comments, quoted
// values and a key on the header line. It deliberately ignores include and
// includeIf directives.
func originURL(config string) (string, bool) {
	var (
		inOrigin bool
		url      string
		found    bool
	)
	for _, line := range strings.FieldsFunc(config, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := headerEnd(line)
			if end < 0 {
				inOrigin = false
				continue
			}
			inOrigin = isOriginSection(line[1:end])
			line = strings.TrimSpace(line[end+1:])
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		if !inOrigin {
			continue
		}
		key, value, hasValue := strings.Cut(line, "=")
		if !strings.EqualFold(strings.TrimSpace(key), "url") || !hasValue {
			continue
		}
		url, found = parseValue(value), true
	}
	return url, found && url != ""
}

// headerEnd returns the index of the ']' that closes the header that starts at
// line[0], skipping ']' inside the quoted subsection; -1 when unterminated.
func headerEnd(line string) int {
	quoted := false
	for i := 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if quoted {
				i++
			}
		case '"':
			quoted = !quoted
		case ']':
			if !quoted {
				return i
			}
		}
	}
	return -1
}

// isOriginSection reports whether a section header body (the text between the
// brackets) names the "origin" remote.
func isOriginSection(h string) bool {
	h = strings.TrimSpace(h)
	// Legacy form [remote.origin]: the whole name is case-insensitive.
	if strings.EqualFold(h, "remote.origin") {
		return true
	}
	name, sub, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(name, "remote") {
		return false
	}
	sub = strings.TrimSpace(sub)
	if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
		return false
	}
	return unescapeSubsection(sub[1:len(sub)-1]) == "origin"
}

// unescapeSubsection resolves the two escapes git allows in a subsection
// name (\" and \\); a backslash before any other character is dropped.
func unescapeSubsection(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseValue decodes a config value: surrounding whitespace is trimmed, double
// quotes group text, backslash escapes \n \t \b \" \\ are resolved, and an
// unquoted # or ; starts a comment.
func parseValue(v string) string {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' && i+1 < len(v):
			i++
			switch v[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'b':
				b.WriteByte('\b')
			default:
				b.WriteByte(v[i])
			}
		case c == '"':
			quoted = !quoted
		case (c == '#' || c == ';') && !quoted:
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}
