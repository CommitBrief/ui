// SPDX-License-Identifier: GPL-3.0-or-later
import type { MessageKey, MessageParams } from "../i18n";

// Pure (no Wails import) so it can be unit-tested without a runtime. The
// backend sends no text: an error is {code, field?, entry?} where `code` is a
// member of the closed set below (internal/domain/repo, repo.Code) and the
// screen words every code itself.

export const repoErrorCodes = [
  "repo_owner_invalid",
  "repo_name_invalid",
  "repo_local_path_invalid",
  "repo_local_path_network",
  "repo_local_path_missing",
  "repo_local_path_not_git",
  "repo_duplicate",
  "repo_not_found",
  "repo_no_remote",
  "repo_remote_unsupported",
  "repo_import_invalid",
  "repo_import_unsupported_schema",
  "repo_import_too_large",
  "canceled",
  "internal",
] as const;
export type RepoErrorCode = (typeof repoErrorCodes)[number];

/** The form fields an error can belong to (`field` of the backend error). */
export type RepoErrorField = "owner" | "name" | "localPath";

/** A classified repo failure; `code` is a member of the backend's closed set (or "internal"). */
export class RepoError extends Error {
  readonly code: string;
  readonly field: string;
  /** 1-based position of the offending record of an import file; 0 when not about an import entry. */
  readonly entry: number;

  constructor(code: string, field = "", entry = 0) {
    super(code);
    this.name = "RepoError";
    this.code = code;
    this.field = field;
    this.entry = entry;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isRepoErrorCode(code: string): code is RepoErrorCode {
  return (repoErrorCodes as readonly string[]).includes(code);
}

/**
 * Reads {code, field, entry} from a rejected binding call (Wails puts the
 * marshalled error in `cause`). Anything unrecognised, including a code that is
 * not in the closed set, becomes "internal", so no unknown text reaches the UI.
 */
export function toRepoError(error: unknown): RepoError {
  if (error instanceof RepoError) return error;
  const cause = isRecord(error) ? error.cause : undefined;
  if (isRecord(cause) && typeof cause.code === "string" && isRepoErrorCode(cause.code)) {
    return new RepoError(
      cause.code,
      typeof cause.field === "string" ? cause.field : "",
      typeof cause.entry === "number" ? cause.entry : 0,
    );
  }
  return new RepoError("internal");
}

const messageKeys: Record<RepoErrorCode, MessageKey> = {
  repo_owner_invalid: "repos.validation.owner_invalid",
  repo_name_invalid: "repos.validation.name_invalid",
  repo_local_path_invalid: "repos.error.repo_local_path_invalid",
  repo_local_path_network: "repos.error.repo_local_path_network",
  repo_local_path_missing: "repos.error.repo_local_path_missing",
  repo_local_path_not_git: "repos.error.repo_local_path_not_git",
  repo_duplicate: "repos.error.repo_duplicate",
  repo_not_found: "repos.error.repo_not_found",
  repo_no_remote: "repos.error.repo_no_remote",
  repo_remote_unsupported: "repos.error.repo_remote_unsupported",
  repo_import_invalid: "repos.error.repo_import_invalid",
  repo_import_unsupported_schema: "repos.error.repo_import_unsupported_schema",
  repo_import_too_large: "repos.error.repo_import_too_large",
  canceled: "repos.error.canceled",
  internal: "repos.error.internal",
};

/** The i18n key of an error code; an unknown code gets the generic sentence. */
export function repoErrorKey(code: string): MessageKey {
  return isRepoErrorCode(code) ? messageKeys[code] : messageKeys.internal;
}

export type Translate = (key: MessageKey, params?: MessageParams) => string;

/** The sentence for an error; an import entry number is put in front of it. */
export function describeRepoError(error: Pick<RepoError, "code" | "entry">, t: Translate): string {
  const message = t(repoErrorKey(error.code));
  return error.entry > 0 ? t("repos.error.entry", { entry: error.entry, message }) : message;
}

/**
 * Which form field an error belongs to: the owner and name errors both sit
 * under the single owner/name input, the path errors under the path field, and
 * everything else (duplicate, not found, internal) is about the whole form.
 */
export function errorFormField(error: Pick<RepoError, "code" | "field">): "slug" | "localPath" | null {
  if (error.field === "owner" || error.field === "name") return "slug";
  if (error.field === "localPath") return "localPath";
  return null;
}
