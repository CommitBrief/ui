// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"
)

type maskEnv struct{ vars []string }

func (e maskEnv) Environ() []string               { return e.vars }
func (e maskEnv) LookupEnv(string) (string, bool) { return "", false }
func (e maskEnv) HomeDir() (string, error)        { return "", nil }

func TestMaskerByShape(t *testing.T) {
	m := NewMasker(nil)
	secretCases := []struct{ name, in, leak string }{
		{"classic GitHub token", "token is ghp_abcdefghijklmnopqrstuvwxyz0123456789 ok", "ghp_abcdefghij"},
		{"fine-grained GitHub token", "github_pat_11ABCDEFG0abcdefghijklmnop_qrstuvwxyz", "github_pat_11ABC"},
		{"GH_TOKEN assignment", "GH_TOKEN=gho_16C7e42F292c6912E7710c838347Ae178B4a", "gho_16C7"},
		{"GITHUB_TOKEN export", "export GITHUB_TOKEN=abcdef123456", "abcdef123456"},
		{"Anthropic key", "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234", "sk-ant-api03"},
		{"OpenAI key", "OPENAI sk-proj-abcdefghijklmnopqrstuvwxyz1234", "sk-proj-abcdefgh"},
		{"Google key", "AIzaSyA1234567890abcdefghijklmnopqrstuv", "AIzaSyA1234"},
		{"xAI key", "xai-abcdefghijklmnopqrstuvwx", "xai-abcdefgh"},
		{"bearer header", "Authorization: Bearer abcdefghij0123456789", "abcdefghij0123456789"},
		{"basic header", "authorization=Basic dXNlcjpwYXNzd29yZA==", "dXNlcjpwYXNzd29yZA"},
		{"bare bearer", "sent Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig", "eyJhbGciOiJIUzI1NiJ9"},
		{"api_key assignment", "api_key: s3cr3t-value", "s3cr3t-value"},
		{"quoted password", `password = "hunter2hunter2"`, "hunter2hunter2"},
		{"JSON api key", `{"providers":{"x":{"api_key":"mistral-0123456789"}}}`, "mistral-0123456789"},
		{"AWS key id", "AKIAABCDEFGHIJKLMNOP", "AKIAABCDEFGH"},
	}
	for _, tc := range secretCases {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Mask(tc.in)
			if strings.Contains(got, tc.leak) {
				t.Errorf("Mask(%q) = %q still holds %q", tc.in, got, tc.leak)
			}
			if !strings.Contains(got, maskText) {
				t.Errorf("Mask(%q) = %q has no mask", tc.in, got)
			}
			if again := m.Mask(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}

	// Ordinary log lines are left alone.
	plain := []string{
		"input_tokens: 4231",
		"max_tokens=1024",
		"reviewing 3 files in main..HEAD",
		"provider anthropic, model claude-opus",
		"cost: $0.0632 (cached)",
		"token limit exceeded",
		"",
	}
	for _, in := range plain {
		if got := m.Mask(in); got != in {
			t.Errorf("Mask(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestMaskerKeepsContext(t *testing.T) {
	m := NewMasker(nil)
	if got, want := m.Mask("Authorization: Bearer abcdefgh12345678"), "Authorization: Bearer "+maskText; got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
	if got, want := m.Mask("GH_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789 gh pr view"), "GH_TOKEN="+maskText+" gh pr view"; got != want {
		t.Errorf("assignment = %q, want %q", got, want)
	}
}

func TestMaskerMasksEnvironmentValues(t *testing.T) {
	m := NewMasker(maskEnv{vars: []string{
		"PATH=/usr/bin",
		"DEEPSEEK_API_KEY=0123456789abcdef0123456789abcdef",
		"SHORT_TOKEN=abc",
		"EDITOR=vimvimvimvim",
		"MY_SECRET=correct-horse-battery",
		"malformed",
	}})
	in := "using 0123456789abcdef0123456789abcdef and correct-horse-battery with abc and vimvimvimvim"
	got := m.Mask(in)
	if strings.Contains(got, "0123456789abcdef") || strings.Contains(got, "correct-horse") {
		t.Errorf("an environment secret leaked: %q", got)
	}
	// A short value and a value of an ordinary variable are not masked.
	if !strings.Contains(got, " abc ") || !strings.Contains(got, "vimvimvimvim") {
		t.Errorf("over-masked: %q", got)
	}
}
