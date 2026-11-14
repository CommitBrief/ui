// SPDX-License-Identifier: GPL-3.0-or-later
import { useState } from "react";
import { CLI_MIN_VERSION, CLI_MIN_VERSION_POSTING, CLI_MIN_VERSION_SECRET_WRITE } from "../../api/cliVersion";
import {
  checkCliUpgrade,
  describeSettingsError,
  toSettingsError,
  upgradeCli,
  type CLIStatus,
  type CLIUpgrade,
} from "../../api/settings";
import { Button, useToast } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import { CopyCommand, Facts, LoadFailed, Loading, Notice, Pill, Section } from "./parts";
import type { Loader } from "./useLoad";
import { useWriteLock } from "./WriteLockContext";
import styles from "./Settings.module.css";

const stateKeys: Record<string, MessageKey> = {
  ok: "settings.cli.state.ok",
  not_found: "settings.cli.state.not_found",
  too_old: "settings.cli.state.too_old",
};

const methodKeys: Record<string, MessageKey> = {
  homebrew: "settings.cli.method.homebrew",
  scoop: "settings.cli.method.scoop",
  "go-install": "settings.cli.method.go-install",
  manual: "settings.cli.method.manual",
};

export function CliVersionSection({ cli }: { cli: Loader<CLIStatus> }) {
  const { t } = useI18n();
  const toast = useToast();
  const write = useWriteLock();
  const [upgrade, setUpgrade] = useState<CLIUpgrade | null>(null);
  const [checking, setChecking] = useState(false);
  const [updating, setUpdating] = useState(false);

  const status = cli.data;

  const check = async () => {
    setChecking(true);
    try {
      setUpgrade(await checkCliUpgrade());
    } catch (error) {
      toast({
        kind: "error",
        title: t("settings.cli.checkFailed"),
        description: describeSettingsError(toSettingsError(error), t),
      });
    } finally {
      setChecking(false);
    }
  };

  const update = async () => {
    const release = write.begin();
    if (!release) return;
    setUpdating(true);
    try {
      const next = await upgradeCli();
      cli.setData(next);
      setUpgrade(null);
      toast({ kind: "success", title: t("settings.cli.updated", { version: next.version }) });
    } catch (error) {
      const failure = toSettingsError(error);
      toast({ kind: "error", title: t("settings.cli.updateFailed"), description: describeSettingsError(failure, t) });
    } finally {
      setUpdating(false);
      release();
    }
  };

  const actions = (
    <>
      <Button icon="updates" loading={checking} disabled={updating || status?.state === "not_found"} onClick={() => void check()}>
        {t("settings.cli.check")}
      </Button>
      <Button variant="ghost" loading={cli.loading && status !== null} onClick={cli.reload}>
        {t("settings.refresh")}
      </Button>
    </>
  );

  let body;
  if (!status) {
    body = cli.error ? <LoadFailed error={cli.error} onRetry={cli.reload} /> : <Loading />;
  } else {
    const version = status.dev
      ? t("settings.cli.version.dev") + (status.commit ? ` (${status.commit.slice(0, 7)})` : "")
      : status.version || t("settings.cli.version.none");
    const capability = (supported: boolean, min: string) =>
      supported ? <Pill tone="ok">{t("settings.cli.cap.yes")}</Pill> : <Pill tone="warning">{t("settings.cli.cap.no", { min })}</Pill>;

    body = (
      <>
        {status.state === "not_found" ? (
          <Notice tone="warning" title={t("settings.cli.notFound.title")}>
            {t("settings.cli.notFound.description")}
          </Notice>
        ) : null}
        {status.state === "too_old" ? (
          <Notice tone="warning" title={t("settings.cli.tooOld.title")}>
            {t("settings.cli.tooOld.description", { version: status.version, min: CLI_MIN_VERSION })}
          </Notice>
        ) : null}
        {cli.error ? <LoadFailed error={cli.error} onRetry={cli.reload} /> : null}
        <Facts
          rows={[
            {
              label: t("settings.cli.version"),
              value: (
                <span className={styles.inlineRow}>
                  <span className={styles.mono}>{version}</span>
                  <Pill tone={status.state === "ok" ? "ok" : "warning"}>{t(stateKeys[status.state] ?? "settings.cli.state.not_found")}</Pill>
                </span>
              ),
            },
            { label: t("settings.cli.min"), value: <span className={styles.mono}>{CLI_MIN_VERSION}</span> },
            ...(status.state === "ok"
              ? [
                  { label: t("settings.cli.cap.secret"), value: capability(status.capabilities.configSetStdin, CLI_MIN_VERSION_SECRET_WRITE) },
                  { label: t("settings.cli.cap.posting"), value: capability(status.capabilities.posting, CLI_MIN_VERSION_POSTING) },
                ]
              : []),
            ...(upgrade && upgrade.method
              ? [{ label: t("settings.cli.method"), value: t(methodKeys[upgrade.method] ?? "settings.cli.method.manual") }]
              : []),
          ]}
        />
        {upgrade ? (
          <div className={styles.block} role="status">
            {upgrade.updateAvailable ? (
              <>
                <p>{t("settings.cli.available", { latest: upgrade.latest, current: upgrade.current })}</p>
                {upgrade.delegated ? (
                  <>
                    <p className={styles.muted}>{t("settings.cli.delegated")}</p>
                    <CopyCommand text={upgrade.action} label={t("settings.cli.command.label")} />
                  </>
                ) : (
                  <div>
                    <Button variant="primary" icon="download" loading={updating} disabled={write.busy && !updating} onClick={() => void update()}>
                      {t("settings.cli.update", { latest: upgrade.latest })}
                    </Button>
                  </div>
                )}
              </>
            ) : (
              <Pill tone="ok">{t("settings.cli.upToDate", { version: upgrade.current })}</Pill>
            )}
          </div>
        ) : (
          <p className={styles.muted}>{t("settings.cli.checkHint")}</p>
        )}
      </>
    );
  }

  return (
    <Section anchor="settings-cli" title={t("settings.cli.title")} description={t("settings.cli.description")} actions={actions}>
      {body}
    </Section>
  );
}
