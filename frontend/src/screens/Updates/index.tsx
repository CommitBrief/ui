// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useState } from "react";
import {
  Code,
  State,
  checkForUpdates,
  installUpdate,
  isUpdateChannel,
  loadUpdates,
  onUpdateStatus,
  setUpdatePreferences,
  toUpdateError,
  updateChannels,
  type Status,
  type UpdatesView,
} from "../../api/updates";
import { Button, PageHeader, Select, Switch } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import styles from "./Updates.module.css";

// Every error code the backend can send has its own sentence; an unknown code
// falls back to the generic one.
const errorKeys: Record<string, MessageKey> = {
  [Code.CodeCheckFailed]: "updates.error.update_check_failed",
  [Code.CodeFeedUnavailable]: "updates.error.update_feed_unavailable",
  [Code.CodeChecksumMissing]: "updates.error.update_checksum_missing",
  [Code.CodeChecksumMismatch]: "updates.error.update_checksum_mismatch",
  [Code.CodeNoArtifact]: "updates.error.update_no_artifact",
  [Code.CodeDownloadFailed]: "updates.error.update_download_failed",
  [Code.CodeInstallFailed]: "updates.error.update_install_failed",
  [Code.CodeRollbackFailed]: "updates.error.update_rollback_failed",
  [Code.CodeDelegated]: "updates.error.update_delegated",
  [Code.CodeNothingToInstall]: "updates.error.update_nothing_to_install",
  [Code.CodeBusy]: "updates.error.update_busy",
  [Code.CodeInvalid]: "updates.error.update_invalid",
  [Code.CodeCanceled]: "updates.error.canceled",
  [Code.CodeInternal]: "updates.error.internal",
};

const delegationKeys: Record<string, MessageKey> = {
  package_manager: "updates.delegated.package_manager",
  not_writable: "updates.delegated.not_writable",
  unbundled: "updates.delegated.unbundled",
  development_build: "updates.delegated.development_build",
  unsupported: "updates.delegated.unsupported",
};

// States while a check or an install is under way: nothing else may start.
const workingStates: readonly State[] = [State.StateChecking, State.StateDownloading, State.StateVerifying, State.StateInstalling, State.StateRestarting];

export function UpdatesScreen() {
  const { t, locale } = useI18n();
  const [view, setView] = useState<UpdatesView | null>(null);
  // The failure of the last action the user started on this screen.
  const [actionError, setActionError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const [installing, setInstalling] = useState(false);

  const applyStatus = useCallback((status: Status) => {
    setView((current) => (current ? { ...current, status } : current));
  }, []);

  useEffect(() => {
    let alive = true;
    // Subscribe first, so a status change that lands while the first load is in
    // flight is not lost; the load then fills in the preferences.
    const unsubscribe = onUpdateStatus(applyStatus);
    loadUpdates()
      .then((next) => {
        if (alive) setView(next);
      })
      .catch((error: unknown) => {
        if (alive) setActionError(toUpdateError(error).code);
      });
    return () => {
      alive = false;
      unsubscribe();
    };
  }, [applyStatus]);

  const check = async () => {
    setActionError(null);
    setChecking(true);
    try {
      setView(await checkForUpdates());
    } catch (error) {
      setActionError(toUpdateError(error).code);
      // The updater's own state (a failed check keeps an earlier find) is the truth.
      loadUpdates().then(setView, () => undefined);
    } finally {
      setChecking(false);
    }
  };

  const install = async () => {
    setActionError(null);
    setInstalling(true);
    try {
      await installUpdate();
      // The restart has been handed over; the app is about to quit.
    } catch (error) {
      setActionError(toUpdateError(error).code);
      loadUpdates().then(setView, () => undefined);
    } finally {
      setInstalling(false);
    }
  };

  const savePreferences = async (channel: string, autoCheck: boolean) => {
    if (!isUpdateChannel(channel)) return;
    setActionError(null);
    try {
      setView(await setUpdatePreferences(channel, autoCheck));
    } catch (error) {
      setActionError(toUpdateError(error).code);
    }
  };

  if (!view) {
    return (
      <>
        <PageHeader title={t("screen.updates.title")} description={t("screen.updates.description")} />
        {actionError ? <ErrorNotice code={actionError} /> : <p className={styles.meta}>{t("common.loading")}</p>}
      </>
    );
  }

  const { status } = view;
  const delegated = status.delegation !== "";
  const working = checking || installing || workingStates.includes(status.state);
  const release = status.available;
  const failedCode = status.state === State.StateFailed ? status.error : "";
  // The error to show: what the user's last action raised, else what the updater
  // remembers (a periodic check that failed, for instance).
  const shownError = actionError ?? (failedCode !== "" ? failedCode : null);

  const formatTime = (iso: string) => new Date(iso).toLocaleString(locale);
  const formatSize = (bytes: number) =>
    `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(bytes / (1024 * 1024))} MB`;

  return (
    <>
      <PageHeader
        title={t("screen.updates.title")}
        description={t("screen.updates.description")}
        actions={
          delegated ? null : (
            <Button icon="updates" loading={checking} disabled={working && !checking} onClick={check}>
              {t("updates.check")}
            </Button>
          )
        }
      />
      <div className={styles.stack}>
        <section className={styles.card} aria-labelledby="updates-current">
          <h2 id="updates-current" className={styles.cardTitle}>
            {t("updates.current")}
          </h2>
          <div className={styles.row}>
            <span className={styles.version}>{status.current}</span>
          </div>
          {delegated ? (
            <div className={`${styles.notice} ${styles.noticeInfo}`} role="note">
              <span className={styles.noticeTitle}>{t("updates.delegated.title")}</span>
              {t(delegationKeys[status.delegation] ?? "updates.delegated.unsupported")}
            </div>
          ) : (
            <p className={styles.meta} role="status">
              {checking || status.state === State.StateChecking
                ? t("updates.checking")
                : view.lastCheck
                  ? `${t("updates.lastCheck", { time: formatTime(view.lastCheck) })}${release ? "" : ` · ${t("updates.upToDate")}`}`
                  : t("updates.notChecked")}
            </p>
          )}
        </section>

        {shownError !== null ? <ErrorNotice code={shownError} unchanged /> : null}

        {!delegated && release ? (
          <section className={styles.card} aria-labelledby="updates-available">
            <div className={styles.row}>
              <h2 id="updates-available" className={styles.cardTitle}>
                {t("updates.available.title", { version: release.version })}
              </h2>
              {release.prerelease ? <span className={styles.pill}>{t("updates.available.prerelease")}</span> : null}
            </div>
            <p className={styles.meta}>
              {t("updates.available.published", { time: formatTime(release.publishedAt) })} ·{" "}
              {t("updates.available.size", { size: formatSize(release.size) })}
            </p>
            <h3>{t("updates.notes")}</h3>
            {/* Plain text only: the notes come from the network. */}
            <pre className={styles.notes}>{release.notes === "" ? t("updates.notes.none") : release.notes}</pre>
            <Progress status={status} />
            <div className={styles.row}>
              <Button variant="primary" icon="updates" loading={installing || isBusyInstall(status.state)} disabled={working && !installing} onClick={install}>
                {t("updates.install")}
              </Button>
            </div>
            <p className={styles.meta}>{t("updates.install.hint")}</p>
          </section>
        ) : null}

        <section className={styles.card} aria-labelledby="updates-prefs">
          <h2 id="updates-prefs" className={styles.cardTitle}>
            {t("updates.prefs.title")}
          </h2>
          <div className={styles.fields}>
            <Select
              label={t("updates.prefs.channel")}
              hint={t("updates.prefs.channel.hint")}
              value={view.channel}
              onChange={(event) => void savePreferences(event.target.value, view.autoCheck)}
              options={updateChannels.map((value) => ({ value, label: t(`updates.prefs.channel.${value}`) }))}
            />
            <Switch
              label={t("updates.prefs.auto")}
              description={t("updates.prefs.auto.description")}
              checked={view.autoCheck}
              disabled={delegated}
              onChange={(event) => void savePreferences(view.channel, event.target.checked)}
            />
          </div>
        </section>
      </div>
    </>
  );
}

function isBusyInstall(state: State): boolean {
  return state === State.StateDownloading || state === State.StateVerifying || state === State.StateInstalling || state === State.StateRestarting;
}

function ErrorNotice({ code, unchanged = false }: { code: string; unchanged?: boolean }) {
  const { t } = useI18n();
  return (
    <div className={`${styles.notice} ${styles.noticeError}`} role="alert">
      <span className={styles.noticeTitle}>{t("updates.error.title")}</span>
      {t(errorKeys[code] ?? "updates.error.internal")}
      {unchanged && code !== Code.CodeRollbackFailed ? ` ${t("updates.error.unchanged")}` : null}
    </div>
  );
}

/** The download bar and the step after it; renders nothing while no install is running. */
function Progress({ status }: { status: Status }) {
  const { t } = useI18n();
  const label = progressLabel(status, t);
  if (label === null) return null;
  const progress = status.state === State.StateDownloading ? status.progress : null;
  const determinate = progress !== null && progress.total > 0;
  return (
    <div role="status" aria-live="polite">
      <progress
        className={styles.progress}
        aria-label={label}
        value={determinate ? progress.written : undefined}
        max={determinate ? progress.total : undefined}
      />
      <p className={styles.meta}>{label}</p>
    </div>
  );
}

function progressLabel(status: Status, t: (key: MessageKey, params?: Record<string, string | number>) => string): string | null {
  switch (status.state) {
    case State.StateDownloading: {
      const p = status.progress;
      return p && p.total > 0
        ? t("updates.progress.downloading", { percent: Math.min(100, Math.floor((p.written / p.total) * 100)) })
        : t("updates.progress.downloadingUnknown");
    }
    case State.StateVerifying:
      return t("updates.progress.verifying");
    case State.StateInstalling:
      return t("updates.progress.installing");
    case State.StateRestarting:
      return t("updates.progress.restarting");
    default:
      return null;
  }
}
