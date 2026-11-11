// SPDX-License-Identifier: GPL-3.0-or-later
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { readStored, writeStored } from "./storage";

export type ThemeMode = "system" | "light" | "dark";
export const themeModes: readonly ThemeMode[] = ["system", "light", "dark"];

const STORAGE_KEY = "commitbrief-ui.theme";

function isThemeMode(value: unknown): value is ThemeMode {
  return value === "system" || value === "light" || value === "dark";
}

/**
 * "system" removes the attribute so tokens.css follows prefers-color-scheme;
 * "light" and "dark" pin the theme.
 */
function applyTheme(mode: ThemeMode): void {
  const root = document.documentElement;
  if (mode === "system") {
    root.removeAttribute("data-theme");
  } else {
    root.setAttribute("data-theme", mode);
  }
}

/** Runs once before the first render so a pinned theme never flashes the other one. */
export function initTheme(): ThemeMode {
  const stored = readStored(STORAGE_KEY);
  const mode = isThemeMode(stored) ? stored : "system";
  applyTheme(mode);
  return mode;
}

interface ThemeValue {
  mode: ThemeMode;
  setMode: (mode: ThemeMode) => void;
}

const ThemeContext = createContext<ThemeValue | null>(null);

export function ThemeProvider({ initial, children }: { initial: ThemeMode; children: ReactNode }) {
  const [mode, setModeState] = useState<ThemeMode>(initial);

  const setMode = useCallback((next: ThemeMode) => {
    applyTheme(next);
    writeStored(STORAGE_KEY, next === "system" ? null : next);
    setModeState(next);
  }, []);

  const value = useMemo(() => ({ mode, setMode }), [mode, setMode]);
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeValue {
  const value = useContext(ThemeContext);
  if (!value) throw new Error("useTheme must be used inside <ThemeProvider>");
  return value;
}
