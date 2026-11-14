// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  JobError,
  cancelRun,
  describeJobError,
  listRuns,
  onRunStatus,
  onTaskStatus,
  toJobError,
  type Run,
} from "../../api/jobs";
import { describeRepoError, listRepos, toRepoError } from "../../api/repos";
import { Button, EmptyState, PageHeader, useToast } from "../../components";
import { useI18n } from "../../i18n";
import { NewJobDialog } from "./NewJobDialog";
import { RunDetail } from "./RunDetail";
import { RunList } from "./RunList";
import { Notice, useNow } from "./parts";
import { indexRepos, isActive, type RepoRef } from "./runView";
import styles from "./Jobs.module.css";

/**
 * Jobs: start a review by hand, follow it live, read past runs and their
 * findings. The list and the detail are two views of one screen (the hash
 * router has no sub-routes); the detail takes the place of the list and
 * "Back" returns focus to the run that was opened.
 */
export function JobsScreen() {
  const { t } = useI18n();
  const toast = useToast();
  const [runs, setRuns] = useState<Run[] | null>(null);
  const [loadError, setLoadError] = useState<JobError | null>(null);
  const [repos, setRepos] = useState<readonly RepoRef[]>([]);
  const [openId, setOpenId] = useState<number | null>(null);
  const [newOpen, setNewOpen] = useState(false);
  const [cancelingIds, setCancelingIds] = useState<ReadonlySet<number>>(new Set());
  const lastOpened = useRef<number | null>(null);

  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const hasActive = runs?.some((run) => isActive(run.status)) ?? false;
  // Only the list needs the shared clock; the detail runs its own, and only while its run is active.
  const now = useNow(hasActive && openId === null);
  const repoIndex = useMemo(() => indexRepos(repos), [repos]);

  /** Reads the history. A failure after the first load keeps what is on screen and says so. */
  const refresh = useCallback(
    (quiet: boolean): Promise<void> =>
      listRuns().then(
        (next) => {
          if (!alive.current) return;
          setRuns(next);
          setLoadError(null);
        },
        (error: unknown) => {
          if (!alive.current) return;
          if (quiet) toast({ kind: "error", title: t("jobs.toast.refreshFailed"), description: describeJobError(toJobError(error), t) });
          else setLoadError(toJobError(error));
        },
      ),
    [t, toast],
  );

  // Subscribe to the status events first, then read, so no change between the two is missed.
  // Bursts of events become one re-read.
  useEffect(() => {
    let timer = 0;
    const reload = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => void refresh(true), 250);
    };
    const offRun = onRunStatus(reload);
    const offTask = onTaskStatus(reload);
    void refresh(false);
    return () => {
      window.clearTimeout(timer);
      offRun();
      offTask();
    };
  }, [refresh]);

  // Repositories name the tasks and fill the "New job" dialog.
  useEffect(() => {
    listRepos().then(
      (list) => {
        if (alive.current) setRepos(list);
      },
      () => undefined, // The dialog says what is missing; the list falls back to "deleted repository".
    );
  }, []);

  // Back from the detail: focus returns to the run that was opened.
  useEffect(() => {
    if (openId !== null || lastOpened.current === null) return;
    const id = lastOpened.current;
    lastOpened.current = null;
    document.getElementById(`run-open-${id}`)?.focus();
  }, [openId]);

  // The repositories are read again when the dialog opens, so one added a moment ago is there.
  const openNew = async () => {
    try {
      const list = await listRepos();
      if (!alive.current) return;
      setRepos(list);
      setNewOpen(true);
    } catch (error) {
      if (alive.current) toast({ kind: "error", title: t("jobs.toast.reposFailed"), description: describeRepoError(toRepoError(error), t) });
    }
  };

  const open = (id: number) => {
    lastOpened.current = id;
    setOpenId(id);
  };

  const cancel = async (run: Run) => {
    setCancelingIds((current) => new Set(current).add(run.id));
    try {
      await cancelRun(run.id);
      if (alive.current) await refresh(true);
    } catch (error) {
      if (alive.current) toast({ kind: "error", title: t("jobs.cancel.failed"), description: describeJobError(toJobError(error), t) });
    } finally {
      if (alive.current) {
        setCancelingIds((current) => {
          const next = new Set(current);
          next.delete(run.id);
          return next;
        });
      }
    }
  };

  if (openId !== null) {
    return <RunDetail runId={openId} repos={repoIndex} onBack={() => setOpenId(null)} />;
  }

  const isEmpty = runs !== null && runs.length === 0;
  const newJob = (
    <Button variant="primary" icon="plus" onClick={() => void openNew()}>
      {t("jobs.new")}
    </Button>
  );

  let body;
  if (loadError) {
    body = (
      <Notice
        tone="error"
        title={t("jobs.loadError.title")}
        live="alert"
        action={
          <Button
            onClick={() => {
              setLoadError(null);
              void refresh(false);
            }}
          >
            {t("jobs.retry")}
          </Button>
        }
      >
        {describeJobError(loadError, t)}
      </Notice>
    );
  } else if (runs === null) {
    body = (
      <p className={styles.muted} role="status">
        {t("common.loading")}
      </p>
    );
  } else if (isEmpty) {
    body = (
      <EmptyState
        icon="jobs"
        title={t("screen.jobs.empty.title")}
        description={t("screen.jobs.empty.description")}
        action={newJob}
      />
    );
  } else {
    body = (
      <RunList
        runs={runs}
        repos={repoIndex}
        now={now}
        cancelingIds={cancelingIds}
        onOpen={(run) => open(run.id)}
        onCancel={(run) => void cancel(run)}
      />
    );
  }

  return (
    <>
      <PageHeader title={t("screen.jobs.title")} description={t("screen.jobs.description")} actions={isEmpty || loadError ? null : newJob} />
      {body}
      {newOpen ? (
        <NewJobDialog
          repos={repos}
          onClose={() => setNewOpen(false)}
          onStarted={(run) => {
            setNewOpen(false);
            void refresh(true);
            open(run.id);
          }}
        />
      ) : null}
    </>
  );
}

