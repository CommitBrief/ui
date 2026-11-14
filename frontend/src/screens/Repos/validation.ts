// SPDX-License-Identifier: GPL-3.0-or-later
import type { MessageKey } from "../../i18n";

// Client-side mirror of internal/domain/repo (ValidateOwner, ValidateName): the
// same shapes the backend and the CLI adapter accept, so an obviously wrong
// entry is answered without a round trip. The backend stays the authority.

export const MAX_OWNER_LENGTH = 100;
export const MAX_NAME_LENGTH = 100;
const ownerPattern = /^[A-Za-z0-9-]+$/;
const namePattern = /^[A-Za-z0-9._-]+$/;

export interface RepoDraft {
  /** The single owner/name input. */
  slug: string;
  localPath: string;
}

export interface DraftErrors {
  slug?: MessageKey;
}

export interface ParsedSlug {
  owner: string;
  name: string;
}

/** Splits "owner/name"; null unless there is exactly one slash and both sides are non-empty. */
export function splitSlug(slug: string): ParsedSlug | null {
  const parts = slug.trim().split("/");
  if (parts.length !== 2) return null;
  const [owner = "", name = ""] = parts.map((part) => part.trim());
  return owner === "" || name === "" ? null : { owner, name };
}

export function joinSlug(owner: string, name: string): string {
  return `${owner}/${name}`;
}

export function validateOwner(owner: string): boolean {
  return owner !== "" && owner.length <= MAX_OWNER_LENGTH && ownerPattern.test(owner);
}

export function validateName(name: string): boolean {
  return name !== "" && name.length <= MAX_NAME_LENGTH && namePattern.test(name) && name !== "." && name !== "..";
}

/** Checks the owner/name input; an empty result means the draft may be sent. */
export function validateDraft(draft: RepoDraft): DraftErrors {
  const slug = draft.slug.trim();
  if (slug === "") return { slug: "repos.validation.slug_required" };
  const parsed = splitSlug(slug);
  if (parsed === null) return { slug: "repos.validation.slug_format" };
  if (!validateOwner(parsed.owner)) return { slug: "repos.validation.owner_invalid" };
  if (!validateName(parsed.name)) return { slug: "repos.validation.name_invalid" };
  return {};
}

/** Whether two owner/name strings name the same repo (GitHub names are case-insensitive). */
export function sameSlug(a: string, b: string): boolean {
  return a.trim().toLowerCase() === b.trim().toLowerCase();
}
