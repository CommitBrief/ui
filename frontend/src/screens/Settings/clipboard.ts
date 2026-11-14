// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Copies text. The async clipboard is tried first; where a webview does not
 * offer it, a throw-away textarea and the legacy copy command are the way out.
 * Resolves false when neither worked.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Falls through: the async clipboard is not available in every webview.
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.className = "sr-only";
  document.body.appendChild(area);
  try {
    area.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    document.body.removeChild(area);
  }
}
