// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useRef, useState, type FormEvent } from "react";
import { describeJobError, startLocalReview, startPRPreview, toJobError, type Run } from "../../api/jobs";
import { Button, Dialog, Input, Select, type SelectOption } from "../../components";
import { useI18n } from "../../i18n";
import { parsePRNumber, parseRange } from "./newJob";
import { Notice } from "./parts";
import type { RepoRef } from "./runView";
import styles from "./Jobs.module.css";

export interface NewJobDialogProps {
  /** The repositories to choose from; the screen loads them. */
  repos: readonly RepoRef[];
  onClose: () => void;
  /** Called with the queued run. The owner closes the dialog and opens the run. */
  onStarted: (run: Run) => void;
}

const FORM_ID = "new-job-form";

type Kind = "local" | "pr";
type LocalMode = "staged" | "unstaged" | "range";

/**
 * Starts a manual job: a local review of a checkout (staged, unstaged or a
 * commit range) or a preview review of a pull request. Both are previews: the
 * dialog says so, and nothing is ever posted to GitHub from here (UI-0008).
 * The screen mounts it only while it is needed, so every opening starts clean.
 */
export function NewJobDialog({ repos, onClose, onStarted }: NewJobDialogProps) {
  const { t } = useI18n();
  const first = repos.find((repo) => repo.localPath !== "") ?? repos[0];
  const [repoId, setRepoId] = useState(first ? String(first.id) : "");
  const [kind, setKind] = useState<Kind>("local");
  const [localMode, setLocalMode] = useState<LocalMode>("staged");
  const [range, setRange] = useState("");
  const [prText, setPrText] = useState("");
  const [repoError, setRepoError] = useState<string | null>(null);
  const [rangeError, setRangeError] = useState<string | null>(null);
  const [prError, setPrError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const rangeRef = useRef<HTMLInputElement>(null);
  const prRef = useRef<HTMLInputElement>(null);

  // Guards setState after the dialog went away while a call was in flight.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  // A start in flight cannot be walked away from: the backend queues the run either way.
  const close = () => {
    if (!busy) onClose();
  };

  const repo = repos.find((item) => String(item.id) === repoId);
  const needsPath = kind === "local";
  const blockedByPath = needsPath && repo !== undefined && repo.localPath === "";

  const repoOptions: SelectOption[] = repos.map((item) => ({
    value: String(item.id),
    label: item.localPath === "" ? `${item.owner}/${item.name} (${t("jobs.new.repo.noPathShort")})` : `${item.owner}/${item.name}`,
  }));

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    setRepoError(null);
    setRangeError(null);
    setPrError(null);
    setFormError(null);
    if (!repo) {
      setRepoError(t("jobs.new.repo.required"));
      return;
    }
    if (blockedByPath) {
      setRepoError(t("jobs.new.repo.blocked"));
      return;
    }
    let call: () => Promise<Run>;
    if (kind === "local") {
      if (localMode === "range") {
        const parsed = parseRange(range);
        if (!parsed.ok) {
          setRangeError(t(parsed.error));
          rangeRef.current?.focus();
          return;
        }
        call = () => startLocalReview({ repositoryId: repo.id, mode: "range", range: parsed.args });
      } else {
        call = () => startLocalReview({ repositoryId: repo.id, mode: localMode, range: null });
      }
    } else {
      const parsed = parsePRNumber(prText);
      if (!parsed.ok) {
        setPrError(t(parsed.error));
        prRef.current?.focus();
        return;
      }
      call = () => startPRPreview({ repositoryId: repo.id, number: parsed.value });
    }
    setBusy(true);
    try {
      const run = await call();
      if (alive.current) onStarted(run);
    } catch (error) {
      if (!alive.current) return;
      const failure = toJobError(error);
      const message = describeJobError(failure, t);
      if (failure.field.startsWith("spec.range")) setRangeError(t("jobs.new.range.invalid"));
      else if (failure.code === "run_repository_not_found" || failure.code === "run_local_path_missing") setRepoError(message);
      else setFormError(message);
    } finally {
      if (alive.current) setBusy(false);
    }
  };

  const noRepos = repos.length === 0;

  return (
    <Dialog
      open
      onClose={close}
      title={t("jobs.new.title")}
      description={t("jobs.new.description")}
      footer={
        <>
          <Button variant="ghost" onClick={close} disabled={busy}>
            {t("jobs.new.cancel")}
          </Button>
          <Button type="submit" form={FORM_ID} variant="primary" loading={busy} disabled={noRepos || blockedByPath}>
            {t("jobs.new.start")}
          </Button>
        </>
      }
    >
      <form id={FORM_ID} className={styles.form} onSubmit={submit} noValidate>
        {formError ? (
          <Notice tone="error" title={t("jobs.new.error.title")} live="alert">
            {formError}
          </Notice>
        ) : null}

        {noRepos ? (
          <Notice tone="warning" title={t("jobs.new.noRepos.title")}>
            {t("jobs.new.noRepos.text")}{" "}
            <a className={styles.link} href="#/repos">
              {t("jobs.next.link.repos")}
            </a>
          </Notice>
        ) : (
          <Select
            label={t("jobs.new.repo.label")}
            value={repoId}
            options={repoOptions}
            error={repoError ?? undefined}
            onChange={(event) => {
              setRepoId(event.target.value);
              setRepoError(null);
            }}
          />
        )}

        {noRepos ? null : (
          <>
            <Select
              label={t("jobs.new.kind.label")}
              value={kind}
              options={[
                { value: "local", label: t("jobs.new.kind.local") },
                { value: "pr", label: t("jobs.new.kind.pr") },
              ]}
              hint={t(kind === "local" ? "jobs.new.kind.local.hint" : "jobs.new.kind.pr.hint")}
              onChange={(event) => {
                setKind(event.target.value === "pr" ? "pr" : "local");
                setRepoError(null);
              }}
            />

            {kind === "local" ? (
              <>
                <Select
                  label={t("jobs.new.mode.label")}
                  value={localMode}
                  options={[
                    { value: "staged", label: t("jobs.new.mode.staged") },
                    { value: "unstaged", label: t("jobs.new.mode.unstaged") },
                    { value: "range", label: t("jobs.new.mode.range") },
                  ]}
                  onChange={(event) => {
                    const value = event.target.value;
                    setLocalMode(value === "range" ? "range" : value === "unstaged" ? "unstaged" : "staged");
                    setRangeError(null);
                  }}
                />
                {localMode === "range" ? (
                  <Input
                    ref={rangeRef}
                    label={t("jobs.new.range.label")}
                    hint={t("jobs.new.range.hint")}
                    error={rangeError ?? undefined}
                    value={range}
                    onChange={(event) => {
                      setRange(event.target.value);
                      setRangeError(null);
                    }}
                    placeholder="main..HEAD"
                    autoComplete="off"
                    autoCapitalize="off"
                    spellCheck={false}
                    className={styles.monoInput}
                    required
                  />
                ) : null}
              </>
            ) : (
              <Input
                ref={prRef}
                label={t("jobs.new.pr.label")}
                hint={t("jobs.new.pr.hint")}
                error={prError ?? undefined}
                value={prText}
                onChange={(event) => {
                  setPrText(event.target.value);
                  setPrError(null);
                }}
                inputMode="numeric"
                autoComplete="off"
                spellCheck={false}
                className={styles.monoInput}
                required
              />
            )}

          </>
        )}

        {blockedByPath ? (
          <Notice tone="warning" title={t("jobs.new.repo.noPath.title")}>
            {t("jobs.new.repo.noPath")}{" "}
            <a className={styles.link} href="#/repos">
              {t("jobs.next.link.repos")}
            </a>
          </Notice>
        ) : null}
        {kind === "pr" && repo !== undefined && repo.localPath === "" ? (
          <Notice tone="warning">{t("jobs.new.pr.noPath")}</Notice>
        ) : null}

        <Notice tone="info" icon="eye" title={t("jobs.mode.preview.title")}>
          {t("jobs.mode.preview.text")}
        </Notice>
      </form>
    </Dialog>
  );
}
