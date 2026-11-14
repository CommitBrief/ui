// SPDX-License-Identifier: GPL-3.0-or-later
import type { MessageKey, MessageParams } from "../i18n";

// Pure (no Wails import) so it can be unit-tested without a runtime. The
// backend sends no text: an error is {code, field?} where `code` is a member of
// the closed set below (internal/domain/settings Code plus the CLI's
// internal/domain/errcode Code) and the screen words every code itself.

export const settingsErrorCodes = [
  // internal/domain/settings
  "settings_key_unknown",
  "settings_key_file_only",
  "settings_secret_not_readable",
  "settings_secret_user_scope_only",
  "settings_value_invalid",
  "settings_value_rejected",
  "settings_scope_invalid",
  "settings_provider_invalid",
  "settings_upgrade_delegated",
  "settings_config_path_unknown",
  "settings_config_file_missing",
  "settings_open_failed",
  // internal/domain/errcode
  "cli_not_found",
  "cli_too_old",
  "gh_not_found",
  "gh_not_authenticated",
  "not_a_repo",
  "provider_error",
  "provider_not_api",
  "self_pr",
  "too_volatile",
  "secret_detected",
  "cost_aborted",
  "timeout",
  "canceled",
  "degraded",
  "capability_missing",
  "unknown",
  "internal",
] as const;
export type SettingsErrorCode = (typeof settingsErrorCodes)[number];

/** A classified settings failure; `code` is a member of the backend's closed set (or "internal"). */
export class SettingsError extends Error {
  readonly code: string;
  /** What the error is about: a config key, or a UI field name such as "cliPath". Empty when it is about the call. */
  readonly field: string;

  constructor(code: string, field = "") {
    super(code);
    this.name = "SettingsError";
    this.code = code;
    this.field = field;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

export function isSettingsErrorCode(code: string): code is SettingsErrorCode {
  return (settingsErrorCodes as readonly string[]).includes(code);
}

/**
 * Reads {code, field} from a rejected binding call (Wails puts the marshalled
 * error in `cause`). Anything unrecognised, including a code that is not in the
 * closed set, becomes "internal", so no unknown text reaches the UI.
 */
export function toSettingsError(error: unknown): SettingsError {
  if (error instanceof SettingsError) return error;
  const cause = isRecord(error) ? error.cause : undefined;
  if (isRecord(cause) && typeof cause.code === "string" && isSettingsErrorCode(cause.code)) {
    return new SettingsError(cause.code, typeof cause.field === "string" ? cause.field : "");
  }
  return new SettingsError("internal");
}

const messageKeys: Record<SettingsErrorCode, MessageKey> = {
  settings_key_unknown: "settings.error.settings_key_unknown",
  settings_key_file_only: "settings.error.settings_key_file_only",
  settings_secret_not_readable: "settings.error.settings_secret_not_readable",
  settings_secret_user_scope_only: "settings.error.settings_secret_user_scope_only",
  settings_value_invalid: "settings.error.settings_value_invalid",
  settings_value_rejected: "settings.error.settings_value_rejected",
  settings_scope_invalid: "settings.error.settings_scope_invalid",
  settings_provider_invalid: "settings.error.settings_provider_invalid",
  settings_upgrade_delegated: "settings.error.settings_upgrade_delegated",
  settings_config_path_unknown: "settings.error.settings_config_path_unknown",
  settings_config_file_missing: "settings.error.settings_config_file_missing",
  settings_open_failed: "settings.error.settings_open_failed",
  cli_not_found: "settings.error.cli_not_found",
  cli_too_old: "settings.error.cli_too_old",
  gh_not_found: "settings.error.gh_not_found",
  gh_not_authenticated: "settings.error.gh_not_authenticated",
  not_a_repo: "settings.error.not_a_repo",
  provider_error: "settings.error.provider_error",
  provider_not_api: "settings.error.provider_not_api",
  self_pr: "settings.error.self_pr",
  too_volatile: "settings.error.too_volatile",
  secret_detected: "settings.error.secret_detected",
  cost_aborted: "settings.error.cost_aborted",
  timeout: "settings.error.timeout",
  canceled: "settings.error.canceled",
  degraded: "settings.error.degraded",
  capability_missing: "settings.error.capability_missing",
  unknown: "settings.error.unknown",
  internal: "settings.error.internal",
};

/** The i18n key of an error code; an unknown code gets the generic sentence. */
export function settingsErrorKey(code: string): MessageKey {
  return isSettingsErrorCode(code) ? messageKeys[code] : messageKeys.internal;
}

export type Translate = (key: MessageKey, params?: MessageParams) => string;

/** The sentence for an error. */
export function describeSettingsError(error: Pick<SettingsError, "code">, t: Translate): string {
  return t(settingsErrorKey(error.code));
}
