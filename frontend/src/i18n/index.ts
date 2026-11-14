// SPDX-License-Identifier: GPL-3.0-or-later
import { createContext, useContext } from "react";
import { readStored, writeStored } from "../app/storage";
import en from "./en.json";
import tr from "./tr.json";

export type Locale = "en" | "tr";
export type MessageKey = keyof typeof en;
export type MessageParams = Record<string, string | number>;

export const locales: readonly Locale[] = ["en", "tr"];

// Compile-time half of the key parity rule (`make i18n-check` is the CI half):
// tr.json must carry exactly the keys of en.json.
const catalogs: Record<Locale, Record<MessageKey, string>> = { en, tr };

const STORAGE_KEY = "commitbrief-ui.locale";

export function isLocale(value: unknown): value is Locale {
  return value === "en" || value === "tr";
}

/** The webview's language when it is one we translate, else English. */
export function systemLocale(): Locale {
  const preferred = typeof navigator === "undefined" ? "" : navigator.language.toLowerCase();
  return preferred.startsWith("tr") ? "tr" : "en";
}

/** Stored choice first, then the webview language, then English. */
export function detectLocale(): Locale {
  const stored = readStored(STORAGE_KEY);
  if (isLocale(stored)) return stored;
  return systemLocale();
}

export function persistLocale(locale: Locale): void {
  writeStored(STORAGE_KEY, locale);
}

/** Looks up a message and fills `{name}` placeholders. Plain text only, never HTML. */
export function translate(locale: Locale, key: MessageKey, params?: MessageParams): string {
  const template = catalogs[locale][key];
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) => {
    const value = params[name];
    return value === undefined ? match : String(value);
  });
}

export interface I18nValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: (key: MessageKey, params?: MessageParams) => string;
}

export const I18nContext = createContext<I18nValue | null>(null);

export function useI18n(): I18nValue {
  const value = useContext(I18nContext);
  if (!value) throw new Error("useI18n must be used inside <I18nProvider>");
  return value;
}
