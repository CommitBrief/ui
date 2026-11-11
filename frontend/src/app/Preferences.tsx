// SPDX-License-Identifier: GPL-3.0-or-later
import { useState } from "react";
import { Button, Dialog, Select } from "../components";
import { locales, useI18n, type Locale } from "../i18n";
import { themeModes, useTheme, type ThemeMode } from "./theme";
import styles from "./Preferences.module.css";

/** Theme and language chooser: one footer button that opens a dialog, so it fits the icon rail too. */
export function Preferences() {
  const { t, locale, setLocale } = useI18n();
  const { mode, setMode } = useTheme();
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="ghost"
        icon="sun"
        className={styles.trigger}
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        title={t("prefs.label")}
      >
        <span className={styles.triggerLabel}>{t("prefs.label")}</span>
      </Button>
      <Dialog open={open} onClose={() => setOpen(false)} title={t("prefs.label")}>
        <div className={styles.stack}>
          <Select
            label={t("theme.label")}
            value={mode}
            onChange={(event) => setMode(event.target.value as ThemeMode)}
            options={themeModes.map((value) => ({ value, label: t(`theme.${value}`) }))}
          />
          <Select
            label={t("language.label")}
            value={locale}
            onChange={(event) => setLocale(event.target.value as Locale)}
            options={locales.map((value) => ({ value, label: t(`language.${value}`) }))}
          />
        </div>
      </Dialog>
    </>
  );
}
