// SPDX-License-Identifier: GPL-3.0-or-later

// The CLI versions the app needs. The Go side keeps the same table in
// internal/adapter/clicmd/version.go (capabilityTable) and decides which
// actions are enabled; these constants only word the version card. A Vitest
// (cliVersion.test.ts) reads that Go table and fails when the two drift.

/** Lowest CLI version the app works with at all. */
export const CLI_MIN_VERSION = "1.21.2";
/** Lowest CLI version that reads `config set <key> -` from stdin, which is how API keys are written. */
export const CLI_MIN_VERSION_SECRET_WRITE = "1.22.0";
/** Lowest CLI version that can post reviews with a capped verdict. */
export const CLI_MIN_VERSION_POSTING = "1.22.0";
