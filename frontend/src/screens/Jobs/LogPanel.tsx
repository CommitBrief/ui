// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { describeJobError, getTaskLog, onTaskLog, toJobError, type JobError } from "../../api/jobs";
import { Button, Icon, Switch, useToast } from "../../components";
import { useI18n } from "../../i18n";
import { copyText } from "../Settings/clipboard";
import { appendCapped, cleanLogLine, mergeLogTail, splitLog, MAX_LOG_LINES } from "./logTail";
import { isActive } from "./runView";
import styles from "./Jobs.module.css";

export interface LogPanelProps {
  taskId: number;
  /** The task's status; a queued or running task streams, a finished one is read once. */
  status: string;
}

/**
 * The log is the CLI's stderr, shown as plain text in a box that follows the tail
 * while the task runs. The owner mounts it with `key={taskId}`, so every task
 * starts from clean state and a status change never restarts the stream.
 */
export function LogPanel({ taskId, status }: LogPanelProps) {
  const { t } = useI18n();
  const toast = useToast();
  const active = isActive(status);
  const [lines, setLines] = useState<string[]>([]);
  const [trimmed, setTrimmed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [missing, setMissing] = useState(false);
  const [error, setError] = useState<JobError | null>(null);
  const [follow, setFollow] = useState(true);
  const bodyRef = useRef<HTMLDivElement>(null);
  const followRef = useRef(true);
  // Was the task still running when this panel was shown? Only then is a final re-read needed.
  const sawActive = useRef(active);
  // The lines on screen, kept in step with `lines`, so a flush never needs a state updater with side effects.
  const linesRef = useRef<string[]>([]);
  const commit = useCallback((next: string[]) => {
    linesRef.current = next;
    setLines(next);
  }, []);

  // Reads the file and merges the live lines that arrived while the read was in flight.
  useEffect(() => {
    let live = true;
    let early: string[] | null = [];
    let pending: string[] = [];
    let frame = 0;

    const flush = () => {
      frame = 0;
      if (!live || pending.length === 0) return;
      const added = pending;
      pending = [];
      const next = appendCapped(linesRef.current, added);
      if (next.dropped) setTrimmed(true);
      commit(next.lines);
    };

    const off = onTaskLog((event) => {
      if (event.taskId !== taskId) return;
      const line = cleanLogLine(event.line);
      if (early) {
        early.push(line);
        return;
      }
      pending.push(line);
      if (frame === 0) frame = window.requestAnimationFrame(flush);
    });

    getTaskLog(taskId).then(
      (view) => {
        if (!live) return;
        const tail = splitLog(view?.text ?? "");
        const merged = mergeLogTail(tail, early ?? []);
        early = null;
        const capped = appendCapped([], merged);
        commit(capped.lines);
        setError(null);
        setTrimmed((view?.truncated ?? false) || capped.dropped);
        setMissing(view === null && merged.length === 0);
        setLoaded(true);
      },
      (failure: unknown) => {
        if (!live) return;
        // The file could not be read, but the live lines still count: show them, with the error above.
        commit(appendCapped([], early ?? []).lines);
        early = null;
        setError(toJobError(failure));
        setLoaded(true);
      },
    );

    return () => {
      live = false;
      off();
      if (frame !== 0) window.cancelAnimationFrame(frame);
    };
  }, [taskId, commit]);

  // When the task finishes while the panel is open, read the file once more: it is the exact record.
  useEffect(() => {
    if (active || !sawActive.current || !loaded) return;
    sawActive.current = false;
    let live = true;
    getTaskLog(taskId).then(
      (view) => {
        if (!live || view === null) return;
        const capped = appendCapped([], splitLog(view.text));
        commit(capped.lines);
        setError(null);
        setTrimmed(view.truncated || capped.dropped);
        setMissing(false);
      },
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [active, loaded, taskId, commit]);

  useLayoutEffect(() => {
    followRef.current = follow;
  }, [follow]);

  // Keep the newest line in view while following.
  useLayoutEffect(() => {
    const body = bodyRef.current;
    if (body && followRef.current) body.scrollTop = body.scrollHeight;
  }, [lines]);

  const onScroll = useCallback(() => {
    const body = bodyRef.current;
    if (!body) return;
    // Scrolling up stops following; reaching the bottom again resumes it.
    const atBottom = body.scrollHeight - body.scrollTop - body.clientHeight < 24;
    if (atBottom !== followRef.current) setFollow(atBottom);
  }, []);

  const copy = async () => {
    const ok = await copyText(lines.join("\n"));
    toast(ok ? { kind: "success", title: t("jobs.log.copied") } : { kind: "error", title: t("jobs.copyFailed") });
  };

  let body;
  if (!loaded) {
    body = (
      <p className={styles.logEmpty} role="status">
        {t("common.loading")}
      </p>
    );
  } else if (lines.length === 0) {
    body = (
      <p className={styles.logEmpty} role={error ? "alert" : undefined}>
        {error ? describeJobError(error, t) : t(active ? "jobs.log.waiting" : missing ? "jobs.log.unavailable" : "jobs.log.empty")}
      </p>
    );
  } else {
    body = (
      <>
        {error ? (
          <p className={styles.logTrim} role="alert">
            {describeJobError(error, t)}
          </p>
        ) : null}
        <LogText lines={lines} trimmed={trimmed} />
      </>
    );
  }

  return (
    <section className={styles.panel} aria-labelledby={`log-title-${taskId}`}>
      <div className={styles.panelHead}>
        <h3 id={`log-title-${taskId}`} className={styles.panelTitle}>
          {t("jobs.log.title")}
        </h3>
        {active ? (
          <span className={styles.live}>
            <Icon name="statusRunning" size={14} className={styles.spin} />
            {t("jobs.log.live")}
          </span>
        ) : null}
        <span className={styles.panelCount}>{t("jobs.log.lines", { count: lines.length })}</span>
        <div className={styles.panelTools}>
          <Switch
            label={t("jobs.log.follow")}
            checked={follow}
            onChange={(event) => {
              setFollow(event.target.checked);
              if (event.target.checked && bodyRef.current) bodyRef.current.scrollTop = bodyRef.current.scrollHeight;
            }}
          />
          <Button size="sm" icon="copy" disabled={lines.length === 0} onClick={() => void copy()}>
            {t("jobs.log.copy")}
          </Button>
        </div>
      </div>
      {/* A focusable scroll box, so the log is reachable and scrollable by keyboard. */}
      <div ref={bodyRef} className={styles.logBody} role="log" aria-label={t("jobs.log.label")} aria-live="off" tabIndex={0} onScroll={onScroll}>
        {body}
      </div>
    </section>
  );
}

/**
 * The log lines as plain text in one text node: the log is the CLI's stderr and
 * can carry content from the code under review, so it is never interpreted as HTML.
 */
export function LogText({ lines, trimmed }: { lines: readonly string[]; trimmed: boolean }) {
  const { t } = useI18n();
  return (
    <>
      {trimmed ? <p className={styles.logTrim}>{t("jobs.log.trimmed", { count: MAX_LOG_LINES })}</p> : null}
      <pre className={styles.logText}>{lines.join("\n")}</pre>
    </>
  );
}
