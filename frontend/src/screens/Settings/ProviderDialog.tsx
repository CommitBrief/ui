// SPDX-License-Identifier: GPL-3.0-or-later
import { useId, useState, type FormEvent } from "react";
import {
  describeSettingsError,
  providerConfig,
  setConfig,
  toSettingsError,
  USER_SCOPE,
  type ProviderView,
} from "../../api/settings";
import { Button, Dialog, Input } from "../../components";
import { useI18n } from "../../i18n";
import { ApiKeyField } from "./ApiKeyField";
import { Loading, Notice } from "./parts";
import { providerKey } from "./providerKeys";
import { useLoad } from "./useLoad";
import { useWriteLock } from "./WriteLockContext";
import { normalizeApiKey } from "./apiKey";
import styles from "./Settings.module.css";

export interface ProviderDialogProps {
  provider: ProviderView;
  /** Whether the installed CLI can take an API key on stdin (CLI 1.22.0 or later). */
  canWriteKey: boolean;
  onClose: () => void;
  /** Called after everything the user changed was written. */
  onSaved: (name: string) => void;
}

/**
 * Model, base URL and API key of one provider, written to the user-level
 * config. The API key field is write-only: it starts empty even when a key is
 * stored, the stored key is never requested, and what is typed is dropped from
 * state the moment it has been handed to the backend.
 */
export function ProviderDialog({ provider, canWriteKey, onClose, onSaved }: ProviderDialogProps) {
  const { t } = useI18n();
  const formId = useId();
  const current = useLoad(() => providerConfig(provider.name));
  const write = useWriteLock();
  // null = untouched: the field then shows what the CLI reported.
  const [model, setModel] = useState<string | null>(null);
  const [baseUrl, setBaseUrl] = useState<string | null>(null);
  const [apiKey, setApiKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loaded = current.data;
  const shownModel = model ?? loaded?.model ?? "";
  const shownBaseUrl = baseUrl ?? loaded?.baseUrl ?? "";
  // What would be written: null when the field is untouched or equals what the CLI reported.
  const modelToWrite = loaded !== null && model !== null && model.trim() !== loaded.model ? model.trim() : null;
  const baseUrlToWrite = loaded !== null && baseUrl !== null && baseUrl.trim() !== loaded.baseUrl ? baseUrl.trim() : null;
  // Surrounding spaces are never part of a key; a field with only spaces is "nothing typed".
  const keyToWrite = normalizeApiKey(apiKey);
  const keyTyped = keyToWrite !== null;

  const close = () => {
    if (saving) return;
    setApiKey("");
    onClose();
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving || loaded === null) return;
    if (modelToWrite === null && baseUrlToWrite === null && !keyTyped) {
      setError(t("settings.providers.dialog.nothing"));
      return;
    }
    const release = write.begin();
    if (!release) return;
    setSaving(true);
    setError(null);
    try {
      if (modelToWrite !== null) await setConfig(providerKey(provider.name, "model"), modelToWrite, USER_SCOPE);
      if (baseUrlToWrite !== null) await setConfig(providerKey(provider.name, "base_url"), baseUrlToWrite, USER_SCOPE);
      if (keyToWrite !== null) {
        const secret = keyToWrite;
        // Dropped from state before the call: nothing keeps it once it is on its way.
        setApiKey("");
        await setConfig(providerKey(provider.name, "api_key"), secret, USER_SCOPE);
      }
      onSaved(provider.name);
    } catch (failure) {
      setError(describeSettingsError(toSettingsError(failure), t));
      setSaving(false);
    } finally {
      release();
    }
  };

  let body;
  if (!loaded) {
    body = current.error ? (
      <Notice
        tone="error"
        title={t("settings.providers.dialog.loadFailed")}
        live="alert"
        action={<Button onClick={current.reload}>{t("settings.retry")}</Button>}
      >
        {describeSettingsError(current.error, t)}
      </Notice>
    ) : (
      <Loading />
    );
  } else {
    body = (
      <form id={formId} className={styles.form} onSubmit={(event) => void submit(event)} noValidate>
        {error ? (
          <Notice tone="error" title={t("settings.providers.dialog.saveFailed", { name: provider.name })} live="alert">
            {error}
          </Notice>
        ) : null}
        <Input
          label={t("settings.providers.dialog.model")}
          hint={t("settings.providers.dialog.model.hint")}
          value={shownModel}
          onChange={(event) => setModel(event.target.value)}
          autoComplete="off"
          spellCheck={false}
        />
        <Input
          label={t("settings.providers.dialog.baseUrl")}
          hint={t("settings.providers.dialog.baseUrl.hint")}
          value={shownBaseUrl}
          onChange={(event) => setBaseUrl(event.target.value)}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
        />
        <ApiKeyField stored={provider.keyInConfig} canWrite={canWriteKey} value={apiKey} onChange={setApiKey} />
      </form>
    );
  }

  return (
    <Dialog
      open
      onClose={close}
      title={t("settings.providers.dialog.title", { name: provider.name })}
      description={t("settings.providers.dialog.description")}
      footer={
        <>
          <Button variant="ghost" onClick={close} disabled={saving}>
            {t("settings.providers.dialog.cancel")}
          </Button>
          <Button type="submit" form={formId} variant="primary" loading={saving} disabled={loaded === null || (write.busy && !saving)}>
            {t("settings.providers.dialog.save")}
          </Button>
        </>
      }
    >
      {body}
    </Dialog>
  );
}
