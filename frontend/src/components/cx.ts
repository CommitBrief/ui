// SPDX-License-Identifier: GPL-3.0-or-later

/** Joins the truthy class names; undefined, null and false are skipped. */
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}
