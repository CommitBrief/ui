// SPDX-License-Identifier: GPL-3.0-or-later
import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { createWriteLock, type WriteLock } from "./writeLock";

interface WriteLockValue {
  /** True while any write on the screen runs: the other write buttons are disabled. */
  busy: boolean;
  /** Takes the turn; null when another write holds it. Call the returned function when done. */
  begin: () => (() => void) | null;
}

const WriteLockContext = createContext<WriteLockValue | null>(null);

export function WriteLockProvider({ children }: { children: ReactNode }) {
  const [busy, setBusy] = useState(false);
  const lock = useMemo<WriteLock>(() => createWriteLock(setBusy), []);
  const value = useMemo<WriteLockValue>(() => ({ busy, begin: lock.tryBegin }), [busy, lock]);
  return <WriteLockContext.Provider value={value}>{children}</WriteLockContext.Provider>;
}

export function useWriteLock(): WriteLockValue {
  const value = useContext(WriteLockContext);
  if (!value) throw new Error("useWriteLock must be used inside <WriteLockProvider>");
  return value;
}
