// SPDX-License-Identifier: GPL-3.0-or-later
import { useState } from "react";
import { cliStatus } from "../../api/settings";
import { Button, PageHeader } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import { AppSection } from "./AppSection";
import { CliConfigSection } from "./CliConfigSection";
import { CliVersionSection } from "./CliVersionSection";
import { GithubSection } from "./GithubSection";
import { ProvidersSection } from "./ProvidersSection";
import { useLoad } from "./useLoad";
import { WriteLockProvider } from "./WriteLockContext";
import styles from "./Settings.module.css";

const sections: readonly { anchor: string; label: MessageKey }[] = [
  { anchor: "settings-cli", label: "settings.nav.cli" },
  { anchor: "settings-providers", label: "settings.nav.providers" },
  { anchor: "settings-github", label: "settings.nav.github" },
  { anchor: "settings-config", label: "settings.nav.config" },
  { anchor: "settings-app", label: "settings.nav.app" },
];

/** Scrolls to a section and moves focus to its heading. Plain buttons, not links: a `#` link would change the hash route. */
function jumpTo(anchor: string): void {
  const section = document.getElementById(anchor);
  if (!section) return;
  const reduce = typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  document.getElementById(`${anchor}-title`)?.focus({ preventScroll: true });
  section.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
}

export function SettingsScreen() {
  const { t } = useI18n();
  const cli = useLoad(cliStatus);
  // Bumped when the CLI path override changes: the CLI-backed sections then read again.
  const [cliEpoch, setCliEpoch] = useState(0);

  const onCliPathChanged = () => {
    setCliEpoch((epoch) => epoch + 1);
    cli.reload();
  };

  return (
    <>
      <PageHeader title={t("screen.settings.title")} description={t("screen.settings.description")} />
      <nav className={styles.nav} aria-label={t("settings.nav.label")}>
        {sections.map((section) => (
          <Button key={section.anchor} variant="ghost" size="sm" onClick={() => jumpTo(section.anchor)}>
            {t(section.label)}
          </Button>
        ))}
      </nav>
      <WriteLockProvider>
        <div className={styles.stack}>
          <CliVersionSection key={`cli-${cliEpoch}`} cli={cli} />
          <ProvidersSection key={`providers-${cliEpoch}`} cli={cli} />
          <GithubSection />
          <CliConfigSection key={`config-${cliEpoch}`} cli={cli} />
          <AppSection onCliPathChanged={onCliPathChanged} />
        </div>
      </WriteLockProvider>
    </>
  );
}
