// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useRef, useState } from "react";
import { onSchedulerStatus, schedulerStatus, type SchedulerStatus } from "../api/schedules";

/** The scheduler's next look moves with the clock, so the status is read again this often besides on every pause or resume. */
const REFRESH_MS = 30_000;
/** A tick is told in seconds, so the clock the line is told against moves this often. */
const CLOCK_MS = 5_000;

/** One read is shared by every view that shows the status, so the bar and the screen never tell two different times. */
const SHARED_EVENT = "commitbrief-ui:scheduler-status";

function share(next: SchedulerStatus): void {
  window.dispatchEvent(new CustomEvent<SchedulerStatus>(SHARED_EVENT, { detail: next }));
}

export interface SchedulerStatusState {
  /** The last status that was read; kept while a read fails. */
  status: SchedulerStatus | null;
  /** The last read failed. */
  failed: boolean;
  /** The clock (ms) to tell the ticks against; moves every few seconds. */
  now: number;
  /** Takes a status the caller already has (the answer of pause or resume). */
  set: (next: SchedulerStatus) => void;
}

/**
 * The scheduler's status for any part of the window: read on mount, on a timer and
 * on the `scheduler:status` event, which the tray's Pause/Resume emits as well, so
 * every view shows the same state.
 */
export function useSchedulerStatus(): SchedulerStatusState {
  const [status, setStatus] = useState<SchedulerStatus | null>(null);
  const [failed, setFailed] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const alive = useRef(true);
  const current = useRef<SchedulerStatus | null>(null);

  const read = useCallback(
    () =>
      schedulerStatus().then(
        (next) => {
          if (!alive.current) return;
          share(next);
        },
        () => {
          if (alive.current) setFailed(true);
        },
      ),
    [],
  );

  useEffect(() => {
    alive.current = true;
    const take = (next: SchedulerStatus) => {
      if (!alive.current) return;
      // A read that was slow comes back after a newer pause or resume: the higher version wins.
      const held = current.current;
      if (held && (next.version ?? 0) < (held.version ?? 0)) return;
      current.current = next;
      setStatus(next);
      setFailed(false);
      setNow(Date.now());
    };
    const onShared = (event: Event) => take((event as CustomEvent<SchedulerStatus>).detail);
    window.addEventListener(SHARED_EVENT, onShared);
    const off = onSchedulerStatus(take);
    void read();
    const interval = window.setInterval(() => void read(), REFRESH_MS);
    const clock = window.setInterval(() => {
      setNow(Date.now());
      // The next look has passed: the numbers on screen are old, take the new ones now, not at the next full refresh.
      const due = current.current?.nextTick ? Date.parse(current.current.nextTick) : NaN;
      if (!Number.isNaN(due) && due <= Date.now()) void read();
    }, CLOCK_MS);
    return () => {
      alive.current = false;
      window.clearInterval(interval);
      window.clearInterval(clock);
      window.removeEventListener(SHARED_EVENT, onShared);
      off();
    };
  }, [read]);

  const set = useCallback((next: SchedulerStatus) => share(next), []);

  return { status, failed, now, set };
}
