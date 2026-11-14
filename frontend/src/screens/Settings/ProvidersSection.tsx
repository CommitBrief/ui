// SPDX-License-Identifier: GPL-3.0-or-later
import { useState } from "react";
import {
  activateProvider,
  describeSettingsError,
  listProviders,
  testProvider,
  toSettingsError,
  type CLIStatus,
  type ProviderView,
} from "../../api/settings";
import { Button, Icon, useToast } from "../../components";
import { useI18n } from "../../i18n";
import { ProviderDialog } from "./ProviderDialog";
import { LoadFailed, Loading, Notice, Pill, Section } from "./parts";
import { useLoad, type Loader } from "./useLoad";
import { useWriteLock } from "./WriteLockContext";
import styles from "./Settings.module.css";

/** Providers come from the CLI, so nothing loads until the CLI is known to be usable. */
export function ProvidersSection({ cli }: { cli: Loader<CLIStatus> }) {
  const { t } = useI18n();
  const status = cli.data;
  let body;
  if (!status) {
    body = cli.error ? <LoadFailed error={cli.error} onRetry={cli.reload} /> : <Loading />;
  } else if (status.state !== "ok") {
    body = <Notice tone="warning">{t("settings.providers.cliNeeded")}</Notice>;
  } else {
    // Keyed by version: a CLI that was updated or re-pointed gets a fresh read.
    body = <ProvidersBody key={status.version} canWriteKey={status.capabilities.configSetStdin} />;
  }
  return (
    <Section anchor="settings-providers" title={t("settings.providers.title")} description={t("settings.providers.description")}>
      {body}
    </Section>
  );
}

function ProvidersBody({ canWriteKey }: { canWriteKey: boolean }) {
  const { t } = useI18n();
  const toast = useToast();
  const providers = useLoad(listProviders);
  const write = useWriteLock();
  const [switching, setSwitching] = useState<string | null>(null);
  const [testing, setTesting] = useState<ReadonlySet<string>>(new Set());
  const [editing, setEditing] = useState<string | null>(null);

  const list = providers.data;
  if (!list) return providers.error ? <LoadFailed error={providers.error} onRetry={providers.reload} /> : <Loading />;

  const use = async (name: string) => {
    const release = write.begin();
    if (!release) return;
    setSwitching(name);
    try {
      await activateProvider(name);
      toast({ kind: "success", title: t("settings.providers.used", { name }) });
      providers.reload();
    } catch (error) {
      toast({ kind: "error", title: t("settings.providers.useFailed"), description: describeSettingsError(toSettingsError(error), t) });
    } finally {
      setSwitching(null);
      release();
    }
  };

  const test = async (name: string) => {
    const hold = (on: boolean) =>
      setTesting((current) => {
        const next = new Set(current);
        if (on) next.add(name);
        else next.delete(name);
        return next;
      });
    hold(true);
    try {
      await testProvider(name);
      toast({ kind: "success", title: t("settings.providers.testOk", { name }) });
    } catch (error) {
      toast({
        kind: "error",
        title: t("settings.providers.testFailed", { name }),
        description: describeSettingsError(toSettingsError(error), t),
      });
    } finally {
      hold(false);
    }
  };

  const editingProvider = editing === null ? null : (list.find((item) => item.name === editing) ?? null);

  return (
    <>
      {providers.error ? <LoadFailed error={providers.error} onRetry={providers.reload} /> : null}
      {list.length === 0 ? (
        <p className={styles.muted}>{t("settings.providers.empty")}</p>
      ) : (
        <ul className={styles.list} aria-label={t("settings.providers.list.label")}>
          {list.map((provider) => (
            <ProviderRow
              key={provider.name}
              provider={provider}
              switching={switching === provider.name}
              writeBusy={write.busy}
              testing={testing.has(provider.name)}
              onUse={() => void use(provider.name)}
              onTest={() => void test(provider.name)}
              onConfigure={() => setEditing(provider.name)}
            />
          ))}
        </ul>
      )}
      {editingProvider ? (
        <ProviderDialog
          provider={editingProvider}
          canWriteKey={canWriteKey}
          onClose={() => setEditing(null)}
          onSaved={(name) => {
            setEditing(null);
            toast({ kind: "success", title: t("settings.providers.dialog.saved", { name }) });
            providers.reload();
          }}
        />
      ) : null}
    </>
  );
}

function ProviderRow({
  provider,
  switching,
  writeBusy,
  testing,
  onUse,
  onTest,
  onConfigure,
}: {
  provider: ProviderView;
  switching: boolean;
  /** Another write on the screen is running: nothing that writes may start. */
  writeBusy: boolean;
  testing: boolean;
  onUse: () => void;
  onTest: () => void;
  onConfigure: () => void;
}) {
  const { t } = useI18n();
  const { name } = provider;
  return (
    <li className={`${styles.listItem} ${provider.active ? styles.listItemActive : ""}`}>
      <div className={styles.listMain}>
        <div className={styles.titleRow}>
          <span className={styles.itemName}>{name}</span>
          {provider.active ? (
            <span className={`${styles.pill} ${styles["pill-ok"]}`}>
              <Icon name="check" size={14} />
              {t("settings.providers.active")}
            </span>
          ) : null}
        </div>
        <p className={styles.muted}>
          {provider.model ? t("settings.providers.model", { model: provider.model }) : t("settings.providers.modelDefault")}
          {" · "}
          {provider.keyInConfig ? <Pill tone="ok">{t("settings.providers.key.set")}</Pill> : <Pill tone="neutral">{t("settings.providers.key.unset")}</Pill>}
        </p>
      </div>
      <div className={styles.listActions}>
        <Button
          size="sm"
          loading={switching}
          disabled={provider.active || writeBusy}
          aria-label={t("settings.providers.use.label", { name })}
          onClick={onUse}
        >
          {t("settings.providers.use")}
        </Button>
        <Button size="sm" loading={testing} aria-label={t("settings.providers.test.label", { name })} onClick={onTest}>
          {t("settings.providers.test")}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          icon="pencil"
          aria-label={t("settings.providers.configure.label", { name })}
          onClick={onConfigure}
        >
          {t("settings.providers.configure")}
        </Button>
      </div>
    </li>
  );
}
