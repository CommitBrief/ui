// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useRef, useState, type FormEvent } from "react";
import { autostartEnabled, describeScheduleError, setAutostart, toScheduleError } from "../../api/schedules";
import { describeSettingsError, getUi, toSettingsError, updateUi, type UI } from "../../api/settings";
import { themeModes, useTheme, type ThemeMode } from "../../app/theme";
import { Button, Input, Select, Switch, useToast } from "../../components";
import { useI18n } from "../../i18n";
import {
  errorField,
  isAppDirty,
  languageChoices,
  resolveLocale,
  toDraft,
  toUi,
  validateApp,
  type AppDraft,
  type AppField,
} from "./appSettings";
import { LoadFailed, Loading, Notice, Section } from "./parts";
import { useLoad } from "./useLoad";
import styles from "./Settings.module.css";

export function AppSection({ onCliPathChanged }: { onCliPathChanged: () => void }) {
  const { t, setLocale } = useI18n();
  const { mode, setMode } = useTheme();
  const toast = useToast();
  const ui = useLoad(getUi);
  // null = untouched: the form then shows what is stored.
  const [edited, setEdited] = useState<AppDraft | null>(null);
  const [saving, setSaving] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<AppField, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);

  const saved = ui.data;
  let body;
  if (!saved) {
    body = ui.error ? <LoadFailed error={ui.error} onRetry={ui.reload} /> : <Loading />;
  } else {
    const draft = edited ?? toDraft(saved);
    const issues = validateApp(draft);
    const dirty = isAppDirty(draft, saved);

    const change = (field: AppField, value: string) => {
      setEdited({ ...draft, [field]: value });
      setFieldErrors((current) => (field in current ? { ...current, [field]: undefined } : current));
    };

    const submit = async (event: FormEvent) => {
      event.preventDefault();
      if (saving || !dirty || Object.keys(issues).length > 0) return;
      setSaving(true);
      setFormError(null);
      setFieldErrors({});
      const pathChanged = draft.cliPath.trim() !== saved.cliPath;
      try {
        const stored: UI = await updateUi(toUi(draft));
        ui.setData(stored);
        setEdited(null);
        // The language the user picked takes effect now; "system" means the webview's own.
        // Only when it changed: saving other settings must not undo a language
        // chosen in the sidebar since.
        if (stored.language !== saved.language) setLocale(resolveLocale(stored.language));
        toast({ kind: "success", title: t("settings.app.saved") });
        if (pathChanged) onCliPathChanged();
      } catch (error) {
        const failure = toSettingsError(error);
        const field = errorField(failure.field);
        if (field !== null && failure.code === "settings_value_invalid") {
          setFieldErrors({ [field]: fieldMessage(field, t) });
        } else {
          setFormError(describeSettingsError(failure, t));
        }
      } finally {
        setSaving(false);
      }
    };

    body = (
      <form className={styles.form} onSubmit={(event) => void submit(event)} noValidate>
        {formError ? (
          <Notice tone="error" title={t("settings.app.saveFailed")} live="alert">
            {formError}
          </Notice>
        ) : null}
        <div className={styles.fields}>
          <Select
            label={t("theme.label")}
            value={mode}
            onChange={(event) => setMode(event.target.value as ThemeMode)}
            options={themeModes.map((value) => ({ value, label: t(`theme.${value}`) }))}
          />
          <Select
            label={t("settings.app.language")}
            hint={t("settings.app.language.hint")}
            value={draft.language}
            disabled={saving}
            onChange={(event) => change("language", event.target.value)}
            options={languageChoices.map((value) => ({
              value,
              label: value === "system" ? t("settings.app.language.system") : t(`language.${value}`),
            }))}
          />
          <Input
            label={t("settings.app.workers")}
            hint={t("settings.app.workers.hint")}
            error={fieldErrors.workers ?? (edited && issues.workers ? t(issues.workers) : undefined)}
            value={draft.workers}
            disabled={saving}
            inputMode="numeric"
            autoComplete="off"
            onChange={(event) => change("workers", event.target.value)}
          />
          <Input
            label={t("settings.app.budget")}
            hint={t("settings.app.budget.hint")}
            error={fieldErrors.dailyBudgetUsd ?? (edited && issues.dailyBudgetUsd ? t(issues.dailyBudgetUsd) : undefined)}
            value={draft.dailyBudgetUsd}
            disabled={saving}
            inputMode="decimal"
            autoComplete="off"
            onChange={(event) => change("dailyBudgetUsd", event.target.value)}
          />
          <Input
            label={t("settings.app.retention")}
            hint={t("settings.app.retention.hint")}
            error={fieldErrors.logRetentionDays ?? (edited && issues.logRetentionDays ? t(issues.logRetentionDays) : undefined)}
            value={draft.logRetentionDays}
            disabled={saving}
            inputMode="numeric"
            autoComplete="off"
            onChange={(event) => change("logRetentionDays", event.target.value)}
          />
        </div>
        <Input
          className={styles.pathInput}
          label={t("settings.app.cliPath")}
          hint={t("settings.app.cliPath.hint")}
          error={fieldErrors.cliPath}
          value={draft.cliPath}
          disabled={saving}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          onChange={(event) => change("cliPath", event.target.value)}
        />
        <div className={styles.saveBar}>
          <Button
            type="submit"
            variant="primary"
            loading={saving}
            disabled={!dirty || Object.keys(issues).length > 0}
          >
            {t("settings.app.save")}
          </Button>
        </div>
      </form>
    );
  }

  return (
    <Section anchor="settings-app" title={t("settings.app.title")} description={t("settings.app.description")}>
      {body}
      <AutostartSwitch />
    </Section>
  );
}

type AutostartState = { kind: "loading" } | { kind: "ready"; on: boolean } | { kind: "unsupported" } | { kind: "failed"; message: string };

/**
 * "Start at login": a login item that opens the app in the tray so schedules run
 * without the user opening it. It takes effect at once (not with the form's Save)
 * and is off until the user turns it on (UI-0002). Where the system has no login
 * item the switch says so and stays off.
 */
function AutostartSwitch() {
  const { t } = useI18n();
  const toast = useToast();
  const [state, setState] = useState<AutostartState>({ kind: "loading" });
  const [busy, setBusy] = useState(false);
  const alive = useRef(true);

  const read = () => {
    autostartEnabled().then(
      (on) => {
        if (alive.current) setState({ kind: "ready", on });
      },
      (error: unknown) => {
        if (!alive.current) return;
        const failure = toScheduleError(error);
        setState(failure.code === "autostart_unsupported" ? { kind: "unsupported" } : { kind: "failed", message: describeScheduleError(failure, t) });
      },
    );
  };

  useEffect(() => {
    alive.current = true;
    read();
    return () => {
      alive.current = false;
    };
    // Mount only: a language change needs no new read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const change = async (next: boolean) => {
    setBusy(true);
    try {
      await setAutostart(next);
      if (alive.current) setState({ kind: "ready", on: next });
    } catch (error) {
      if (alive.current) toast({ kind: "error", title: t("settings.app.autostart.failed"), description: describeScheduleError(toScheduleError(error), t) });
    } finally {
      if (alive.current) setBusy(false);
    }
  };

  if (state.kind === "failed") {
    const retry = () => {
      setState({ kind: "loading" });
      read();
    };
    return (
      <div className={styles.autostart}>
        <Notice
          tone="error"
          title={t("settings.app.autostart.readFailed")}
          live="alert"
          action={
            <Button size="sm" onClick={retry}>
              {t("schedules.retry")}
            </Button>
          }
        >
          {state.message}
        </Notice>
      </div>
    );
  }

  return (
    <div className={styles.autostart}>
      <Switch
        label={t("settings.app.autostart")}
        description={state.kind === "unsupported" ? t("settings.app.autostart.unsupported") : t("settings.app.autostart.description")}
        checked={state.kind === "ready" && state.on}
        disabled={state.kind !== "ready" || busy}
        onChange={(event) => void change(event.target.checked)}
      />
    </div>
  );
}

/** The sentence for a field the backend refused. */
function fieldMessage(field: AppField, t: ReturnType<typeof useI18n>["t"]): string {
  switch (field) {
    case "cliPath":
      return t("settings.app.cliPath.error");
    case "workers":
      return t("settings.app.workers.error");
    case "dailyBudgetUsd":
      return t("settings.app.budget.error");
    case "logRetentionDays":
      return t("settings.app.retention.error");
    default:
      return t("settings.error.settings_value_invalid");
  }
}
