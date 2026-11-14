// SPDX-License-Identifier: GPL-3.0-or-later
import { CLI_MIN_VERSION_SECRET_WRITE } from "../../api/cliVersion";
import { Input } from "../../components";
import { useI18n } from "../../i18n";

export interface ApiKeyFieldProps {
  /** Whether the CLI's config file holds a key for this provider. Only this fact is known: the key itself never reaches the UI. */
  stored: boolean;
  /** Whether the installed CLI can take a key on stdin (CLI 1.22.0 or later). */
  canWrite: boolean;
  /** What the user has typed in this session; the field never shows anything else. */
  value: string;
  onChange: (value: string) => void;
}

/**
 * The write-only API key field. It starts empty even when a key is stored,
 * says "stored" or "not stored" in words, and is a new-password field so the
 * webview and password managers do not offer to fill it in or remember it.
 */
export function ApiKeyField({ stored, canWrite, value, onChange }: ApiKeyFieldProps) {
  const { t } = useI18n();
  return (
    <Input
      type="password"
      label={t("settings.providers.dialog.apiKey")}
      hint={
        canWrite
          ? t(stored ? "settings.providers.dialog.apiKey.set" : "settings.providers.dialog.apiKey.unset")
          : t("settings.providers.dialog.apiKey.needsCli", { min: CLI_MIN_VERSION_SECRET_WRITE })
      }
      placeholder={stored ? t("settings.providers.dialog.apiKey.placeholder") : undefined}
      value={value}
      onChange={(event) => onChange(event.target.value)}
      disabled={!canWrite}
      autoComplete="new-password"
      autoCapitalize="off"
      spellCheck={false}
    />
  );
}
