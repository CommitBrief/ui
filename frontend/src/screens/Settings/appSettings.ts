// SPDX-License-Identifier: GPL-3.0-or-later
import type { UI } from "../../api/settings";
import { isLocale, systemLocale, type Locale, type MessageKey } from "../../i18n";

// Pure helpers of the application settings form. The backend
// (internal/domain/settings UI.Validate) checks every write again; the bounds
// below mirror its constants so a typo is caught before the round trip.

export const WORKERS_MIN = 1;
export const WORKERS_MAX = 4;
export const BUDGET_MAX = 10000;
export const RETENTION_MIN = 1;
export const RETENTION_MAX = 365;

export const languageChoices = ["system", "en", "tr"] as const;

/** What the user has typed: every field as text, so a half-typed number is representable. */
export interface AppDraft {
  language: string;
  cliPath: string;
  workers: string;
  dailyBudgetUsd: string;
  logRetentionDays: string;
}

export type AppField = keyof AppDraft;

export function toDraft(ui: UI): AppDraft {
  return {
    language: ui.language,
    cliPath: ui.cliPath,
    workers: String(ui.workers),
    dailyBudgetUsd: String(ui.dailyBudgetUsd),
    logRetentionDays: String(ui.logRetentionDays),
  };
}

const wholeNumber = /^\d+$/;
const amount = /^(\d+\.?\d*|\.\d+)$/;

/** Early validation; a field absent from the result is acceptable. */
export function validateApp(draft: AppDraft): Partial<Record<AppField, MessageKey>> {
  const issues: Partial<Record<AppField, MessageKey>> = {};
  const workers = draft.workers.trim();
  if (!wholeNumber.test(workers) || Number(workers) < WORKERS_MIN || Number(workers) > WORKERS_MAX) {
    issues.workers = "settings.app.workers.error";
  }
  const budget = draft.dailyBudgetUsd.trim();
  if (!amount.test(budget) || Number(budget) > BUDGET_MAX) issues.dailyBudgetUsd = "settings.app.budget.error";
  const days = draft.logRetentionDays.trim();
  if (!wholeNumber.test(days) || Number(days) < RETENTION_MIN || Number(days) > RETENTION_MAX) {
    issues.logRetentionDays = "settings.app.retention.error";
  }
  return issues;
}

/** The settings the backend takes; call only when validateApp found nothing. */
export function toUi(draft: AppDraft): UI {
  return {
    language: draft.language,
    cliPath: draft.cliPath.trim(),
    workers: Number(draft.workers.trim()),
    dailyBudgetUsd: Number(draft.dailyBudgetUsd.trim()),
    logRetentionDays: Number(draft.logRetentionDays.trim()),
  };
}

export function isAppDirty(draft: AppDraft, saved: UI): boolean {
  const base = toDraft(saved);
  return (Object.keys(base) as AppField[]).some((field) => draft[field].trim() !== base[field].trim());
}

/** The interface locale a stored language choice means: "system" follows the webview. */
export function resolveLocale(language: string): Locale {
  return isLocale(language) ? language : systemLocale();
}

/** The form field a backend error is about, or null when it concerns the whole save. */
export function errorField(field: string): AppField | null {
  return field === "language" || field === "cliPath" || field === "workers" || field === "dailyBudgetUsd" || field === "logRetentionDays"
    ? field
    : null;
}
