// SPDX-License-Identifier: GPL-3.0-or-later

// localStorage can throw (blocked site data) or be absent; the UI must render
// without it, so every access is guarded.

export function readStored(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStored(key: string, value: string | null): void {
  try {
    if (value === null) {
      window.localStorage.removeItem(key);
    } else {
      window.localStorage.setItem(key, value);
    }
  } catch {
    // Preference simply does not persist.
  }
}
