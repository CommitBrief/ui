// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The API key to write, or null when nothing real was typed. Spaces around a
 * pasted key (a trailing space from a copy) are never part of it, and a field
 * holding only spaces would otherwise replace a stored key with blanks. Only
 * the edges are trimmed; the CLI itself drops one trailing newline.
 */
export function normalizeApiKey(typed: string): string | null {
  const key = typed.trim();
  return key === "" ? null : key;
}
