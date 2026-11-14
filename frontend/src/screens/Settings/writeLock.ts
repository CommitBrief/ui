// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * One turn at a time for everything on the Settings screen that changes the
 * CLI's configuration (config keys, the active provider, a provider's model and
 * key, a CLI update). The CLI reads a config file, changes it and writes it back
 * without locking, so two overlapping writes can drop one change silently; the
 * backend lines them up too (app.Settings), and the screen keeps the other
 * write buttons disabled so the user is never left wondering.
 *
 * Pure (no React) so it is unit-tested; WriteLockContext.tsx puts it on screen.
 */
export interface WriteLock {
  /** Takes the turn. Returns the function that gives it back, or null when someone else holds it. */
  tryBegin: () => (() => void) | null;
  isBusy: () => boolean;
}

export function createWriteLock(onChange: (busy: boolean) => void = () => undefined): WriteLock {
  let busy = false;
  return {
    tryBegin() {
      if (busy) return null;
      busy = true;
      onChange(true);
      let released = false;
      return () => {
        if (released) return;
        released = true;
        busy = false;
        onChange(false);
      };
    },
    isBusy: () => busy,
  };
}
