// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { CLI_MIN_VERSION_POSTING } from "../../api/cliVersion";
import type { Repository } from "../../api/repos";
import {
  MAX_NAME_LENGTH,
  MAX_WINDOWS,
  describeScheduleError,
  errorTarget,
  saveBotJob,
  toScheduleError,
  type ErrorTarget,
  type ScheduledJob,
} from "../../api/schedules";
import { Button, Dialog, Icon, Input, Select, Switch } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import { Notice } from "../Jobs/parts";
import { PostConsentDialog } from "./PostConsentDialog";
import {
  DAY_BITS,
  consentRecorded,
  dayLongKey,
  dayShortKey,
  emptyDraft,
  hasIssues,
  newWindowDraft,
  saveBlocker,
  systemTimeZone,
  toDraft,
  toInput,
  validateDraft,
  type ScheduleDraft,
  type ScheduleMode,
  type WindowDraft,
  type WindowIssues,
} from "./schedule";
import styles from "./Schedules.module.css";

export interface ScheduleFormProps {
  /** The job to edit; null creates one. */
  job: ScheduledJob | null;
  repos: readonly Repository[];
  /** The installed CLI can post (capabilities.posting). */
  posting: boolean;
  /** The CLI status could not be read at all, so `posting` is a guess of false. */
  cliUnknown: boolean;
  onClose: () => void;
  /** Called with the stored job. The owner closes the form. */
  onSaved: (job: ScheduledJob) => void;
  /** Starts from this draft instead of the job (tests render a draft that is mid-edit). */
  initialDraft?: ScheduleDraft;
}

const FORM_ID = "schedule-form";
const ZONE_LIST_ID = "schedule-zone-list";

const windowFieldMessage: Record<string, MessageKey> = {
  days: "schedules.form.days.required",
  start: "schedules.form.time.invalid",
  end: "schedules.form.time.invalid",
  every: "schedules.form.every.invalid",
  tz: "schedules.form.tz.invalid",
};

let zoneNames: string[] | null = null;
/** IANA names for the time zone suggestions; empty where the runtime cannot list them (typing still works). */
function knownZones(): string[] {
  if (zoneNames === null) {
    try {
      zoneNames = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
    } catch {
      zoneNames = [];
    }
    if (!zoneNames.includes("UTC")) zoneNames = [...zoneNames, "UTC"];
  }
  return zoneNames;
}

/**
 * Create or edit a bot schedule: repos, one or more time windows (days, from,
 * until, interval, zone), draft handling, enabled and the mode. Preview is the
 * default. Choosing post opens the consent dialog; without the consent (or an
 * earlier recorded one) the Save button stays disabled, and so it does when the
 * installed CLI cannot post, where the post option is off as well (UI-0008).
 * The owner mounts it only while it is needed, so every opening starts clean.
 */
export function ScheduleForm({ job, repos, posting, cliUnknown, onClose, onSaved, initialDraft }: ScheduleFormProps) {
  const { t } = useI18n();
  const ids = useId();
  const [zone] = useState(systemTimeZone);
  const [draft, setDraft] = useState<ScheduleDraft>(() => initialDraft ?? (job ? toDraft(job, zone) : emptyDraft(zone)));
  const [showIssues, setShowIssues] = useState(false);
  const [focusTick, setFocusTick] = useState(0);
  const [serverTarget, setServerTarget] = useState<ErrorTarget | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [consentOpen, setConsentOpen] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);

  // Guards setState after the dialog went away while a call was in flight.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  // After a refused submit, focus goes to the first field that says why.
  useEffect(() => {
    if (focusTick === 0) return;
    formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }, [focusTick]);

  const issues = validateDraft(draft, MAX_NAME_LENGTH);
  const recorded = consentRecorded(job);
  const blocker = saveBlocker(draft, { consentRecorded: recorded, posting });
  const noRepos = repos.length === 0;

  const edit = (next: Partial<ScheduleDraft>) => {
    setDraft((current) => ({ ...current, ...next }));
    setServerTarget(null);
    setFormError(null);
  };
  const editWindow = (key: number, next: Partial<WindowDraft>) => {
    setDraft((current) => ({ ...current, windows: current.windows.map((w) => (w.key === key ? { ...w, ...next } : w)) }));
    setServerTarget(null);
    setFormError(null);
  };

  const close = () => {
    if (!busy) onClose();
  };

  const chooseMode = (mode: ScheduleMode) => {
    if (mode === draft.mode) return;
    if (mode === "preview") {
      // A later switch back to post asks again, as the backend does for a stored job that went back to preview.
      edit({ mode, consented: false });
      return;
    }
    edit({ mode });
    if (!recorded) setConsentOpen(true);
  };

  const toggleTarget = (id: number, on: boolean) => {
    edit({ targets: on ? [...draft.targets, id] : draft.targets.filter((value) => value !== id) });
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy || blocker !== null || noRepos) return;
    if (hasIssues(issues)) {
      setShowIssues(true);
      setFocusTick((tick) => tick + 1);
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      const saved = await saveBotJob(toInput(draft, job?.id ?? 0));
      if (alive.current) onSaved(saved);
    } catch (error) {
      if (!alive.current) return;
      const failure = toScheduleError(error);
      const target = errorTarget(failure);
      setServerTarget(target);
      if (target === null) setFormError(describeScheduleError(failure, t));
      else setFocusTick((tick) => tick + 1);
    } finally {
      if (alive.current) setBusy(false);
    }
  };

  // What a field says: its own validation once the user tried to save, else what the backend refused.
  const nameError: MessageKey | null = showIssues ? (issues.name ?? null) : serverTarget?.kind === "name" ? "schedules.form.name.required" : null;
  const targetsError: MessageKey | null = showIssues ? (issues.targets ?? null) : serverTarget?.kind === "targets" ? "schedules.form.targets.required" : null;
  const windowIssue = (index: number, field: keyof WindowIssues): MessageKey | null => {
    if (showIssues) return issues.windows[index]?.[field] ?? null;
    if (serverTarget?.kind === "window" && serverTarget.index === index && serverTarget.field === field) {
      return windowFieldMessage[field] ?? null;
    }
    return null;
  };
  const windowsError: MessageKey | null = serverTarget?.kind === "windows" ? "schedules.form.windows.hint" : null;

  const consented = draft.mode === "post" && (recorded || draft.consented);

  return (
    <>
      <Dialog
        open
        onClose={close}
        title={t(job ? "schedules.form.title.edit" : "schedules.form.title.new")}
        description={t("schedules.form.description")}
        footer={
          <>
            <Button variant="ghost" onClick={close} disabled={busy}>
              {t("schedules.cancel")}
            </Button>
            <Button type="submit" form={FORM_ID} variant="primary" loading={busy} disabled={noRepos || blocker !== null}>
              {t("schedules.form.save")}
            </Button>
          </>
        }
      >
        <form id={FORM_ID} ref={formRef} className={styles.form} onSubmit={(event) => void submit(event)} noValidate>
          {formError ? (
            <Notice tone="error" title={t("schedules.form.error.title")} live="alert">
              {formError}
            </Notice>
          ) : null}

          <Select
            label={t("schedules.form.kind")}
            value="bot"
            disabled
            options={[{ value: "bot", label: t("schedules.form.kind.bot") }]}
            onChange={() => undefined}
          />

          <Input
            label={t("schedules.form.name")}
            hint={t("schedules.form.name.hint")}
            error={nameError ? t(nameError) : undefined}
            value={draft.name}
            onChange={(event) => edit({ name: event.target.value })}
            autoComplete="off"
            required
          />

          <fieldset className={styles.fieldset} aria-describedby={`${ids}-targets-hint`}>
            <legend className={styles.legend}>{t("schedules.form.targets")}</legend>
            <p id={`${ids}-targets-hint`} className={styles.hint}>
              {t("schedules.form.targets.hint")}
            </p>
            {noRepos ? (
              <Notice tone="warning">
                {t("schedules.form.targets.noRepos")}{" "}
                <a className={styles.link} href="#/repos">
                  {t("jobs.next.link.repos")}
                </a>
              </Notice>
            ) : (
              <ul className={styles.repoList}>
                {repos.map((repo, index) => (
                  <li key={repo.id}>
                    <label className={styles.repoOption}>
                      <input
                        type="checkbox"
                        checked={draft.targets.includes(repo.id)}
                        aria-invalid={targetsError && index === 0 ? true : undefined}
                        onChange={(event) => toggleTarget(repo.id, event.target.checked)}
                      />
                      <span className={styles.repoName}>
                        {repo.owner}/{repo.name}
                      </span>
                      {repo.localPath === "" ? <span className={styles.repoNote}>{t("schedules.form.targets.noPath")}</span> : null}
                    </label>
                  </li>
                ))}
              </ul>
            )}
            {targetsError ? (
              <p className={styles.fieldError} role="alert">
                {t(targetsError)}
              </p>
            ) : null}
          </fieldset>

          <fieldset className={styles.fieldset}>
            <legend className={styles.legend}>{t("schedules.form.windows")}</legend>
            <p className={windowsError ? styles.fieldError : styles.hint}>{t("schedules.form.windows.hint")}</p>
            <datalist id={ZONE_LIST_ID}>
              {knownZones().map((name) => (
                <option key={name} value={name} />
              ))}
            </datalist>
            {draft.windows.map((w, index) => {
              const titleId = `${ids}-window-${w.key}`;
              const daysIssue = windowIssue(index, "days");
              return (
                <div key={w.key} className={styles.windowGroup} role="group" aria-labelledby={titleId}>
                  <div className={styles.windowHead}>
                    <p id={titleId} className={styles.windowTitle}>
                      {t("schedules.form.window.title", { n: index + 1 })}
                    </p>
                    {draft.windows.length > 1 ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        icon="trash"
                        aria-label={t("schedules.form.window.remove.aria", { n: index + 1 })}
                        onClick={() => edit({ windows: draft.windows.filter((other) => other.key !== w.key) })}
                      >
                        {t("schedules.form.window.remove")}
                      </Button>
                    ) : null}
                  </div>

                  <fieldset className={styles.fieldset}>
                    <legend className={styles.legend}>{t("schedules.form.days")}</legend>
                    <div className={styles.dayChips}>
                      {DAY_BITS.map((bit, dayIndex) => (
                        <label key={bit} className={styles.chip}>
                          <input
                            type="checkbox"
                            aria-label={t(dayLongKey(dayIndex))}
                            checked={(w.days & bit) !== 0}
                            aria-invalid={daysIssue && dayIndex === 0 ? true : undefined}
                            onChange={(event) => editWindow(w.key, { days: event.target.checked ? w.days | bit : w.days & ~bit })}
                          />
                          <Icon name="check" size={14} className={styles.chipCheck} />
                          <span aria-hidden="true">{t(dayShortKey(dayIndex))}</span>
                        </label>
                      ))}
                    </div>
                    {daysIssue ? (
                      <p className={styles.fieldError} role="alert">
                        {t(daysIssue)}
                      </p>
                    ) : null}
                  </fieldset>

                  <div className={styles.windowGrid}>
                    <Input
                      type="time"
                      label={t("schedules.form.start")}
                      value={w.start}
                      error={windowIssue(index, "start") ? t(windowIssue(index, "start") as MessageKey) : undefined}
                      onChange={(event) => editWindow(w.key, { start: event.target.value })}
                      required
                    />
                    <Input
                      type="time"
                      label={t("schedules.form.end")}
                      hint={t("schedules.form.end.hint")}
                      value={w.end}
                      error={windowIssue(index, "end") ? t(windowIssue(index, "end") as MessageKey) : undefined}
                      onChange={(event) => editWindow(w.key, { end: event.target.value })}
                      required
                    />
                    <Input
                      label={t("schedules.form.every")}
                      value={w.every}
                      inputMode="numeric"
                      autoComplete="off"
                      error={windowIssue(index, "every") ? t(windowIssue(index, "every") as MessageKey) : undefined}
                      onChange={(event) => editWindow(w.key, { every: event.target.value })}
                      required
                    />
                    <Input
                      className={`${styles.windowZone} ${styles.monoInput}`}
                      label={t("schedules.form.tz")}
                      hint={t("schedules.form.tz.hint")}
                      list={ZONE_LIST_ID}
                      value={w.tz}
                      autoComplete="off"
                      autoCapitalize="off"
                      spellCheck={false}
                      error={windowIssue(index, "tz") ? t(windowIssue(index, "tz") as MessageKey) : undefined}
                      onChange={(event) => editWindow(w.key, { tz: event.target.value })}
                      required
                    />
                  </div>
                </div>
              );
            })}
            {draft.windows.length < MAX_WINDOWS ? (
              <div>
                <Button variant="secondary" size="sm" icon="plus" onClick={() => edit({ windows: [...draft.windows, newWindowDraft(zone)] })}>
                  {t("schedules.form.window.add")}
                </Button>
              </div>
            ) : null}
          </fieldset>

          <Switch
            label={t("schedules.form.skipDrafts")}
            description={t("schedules.form.skipDrafts.description")}
            checked={draft.skipDrafts}
            onChange={(event) => edit({ skipDrafts: event.target.checked })}
          />
          <Switch
            label={t("schedules.form.enabled")}
            description={t("schedules.form.enabled.description")}
            checked={draft.enabled}
            onChange={(event) => edit({ enabled: event.target.checked })}
          />

          <fieldset className={styles.fieldset}>
            <legend className={styles.legend}>{t("schedules.form.mode")}</legend>
            <div className={styles.modeOptions}>
              <label className={styles.modeOption}>
                <input type="radio" name={`${ids}-mode`} value="preview" checked={draft.mode === "preview"} onChange={() => chooseMode("preview")} />
                <span className={styles.modeOptionBody}>
                  <span className={styles.modeOptionTitle}>
                    <Icon name="eye" size={14} />
                    {t("schedules.form.mode.preview")}
                  </span>
                  <span className={styles.modeOptionText}>{t("schedules.form.mode.preview.description")}</span>
                </span>
              </label>
              <label className={`${styles.modeOption} ${styles.modePost}`}>
                <input
                  type="radio"
                  name={`${ids}-mode`}
                  value="post"
                  checked={draft.mode === "post"}
                  disabled={!posting && draft.mode !== "post"}
                  onChange={() => chooseMode("post")}
                />
                <span className={styles.modeOptionBody}>
                  <span className={styles.modeOptionTitle}>
                    <Icon name="send" size={14} />
                    {t("schedules.form.mode.post")}
                  </span>
                  <span className={styles.modeOptionText}>{t("schedules.form.mode.post.description")}</span>
                </span>
              </label>
            </div>
            {!posting ? (
              <Notice tone="warning" title={cliUnknown ? undefined : t("schedules.form.mode.cliOld.title")}>
                <span>
                  {cliUnknown ? t("schedules.form.mode.cliUnknown.text") : t("schedules.form.mode.cliOld.text", { version: CLI_MIN_VERSION_POSTING })}
                  {draft.mode === "post" ? ` ${t("schedules.form.mode.cliBlocked")}` : ""}
                </span>
                <p>
                  <a className={styles.link} href="#/settings">
                    {t(cliUnknown ? "jobs.next.link.settings" : "schedules.form.mode.cliOld.action")}
                  </a>
                </p>
              </Notice>
            ) : consented ? (
              <Notice tone="info" icon="send">
                {t("schedules.form.mode.consented")}
              </Notice>
            ) : null}
          </fieldset>
        </form>
      </Dialog>

      {consentOpen ? (
        <PostConsentDialog
          onConfirm={() => {
            edit({ consented: true });
            setConsentOpen(false);
          }}
          onCancel={() => {
            // Declined: the mode goes back to preview, so nothing is left half-chosen.
            edit({ mode: "preview", consented: false });
            setConsentOpen(false);
          }}
        />
      ) : null}
    </>
  );
}
