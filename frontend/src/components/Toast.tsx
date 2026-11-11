// SPDX-License-Identifier: GPL-3.0-or-later
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { cx } from "./cx";
import { Icon, type IconName } from "./Icon";
import { subscribeModals } from "./modalEvents";
import styles from "./Toast.module.css";

export type ToastKind = "success" | "error" | "info";

export interface ToastInput {
  kind?: ToastKind;
  title: string;
  description?: string;
}

interface ToastItem extends ToastInput {
  id: number;
  kind: ToastKind;
}

type ShowToast = (toast: ToastInput) => void;

const ToastContext = createContext<ShowToast | null>(null);

// What the announcers speak. `live` follows the toasts immediately; `delivered`
// follows them only while no modal dialog is open (see ToastProvider).
interface AnnouncementsValue {
  live: readonly ToastItem[];
  delivered: readonly ToastItem[];
  /** Called by a dialog announcer for toasts it spoke while the dialog stayed open. */
  markSpoken: (ids: readonly number[]) => void;
}
const AnnouncementsContext = createContext<AnnouncementsValue | null>(null);

const AUTO_DISMISS_MS = 6000;
const MAX_VISIBLE = 4;
// Errors persist, so they are only evicted when errors alone overflow this cap.
const MAX_ERRORS = 8;

/**
 * Keeps the list bounded without losing an unread error: when over the cap the
 * oldest non-error toast goes first. Errors are only trimmed past MAX_ERRORS.
 */
function trim(items: ToastItem[]): ToastItem[] {
  const next = [...items];
  while (next.length > MAX_VISIBLE) {
    const index = next.findIndex((item) => item.kind !== "error");
    if (index === -1) break;
    next.splice(index, 1);
  }
  return next.length > MAX_ERRORS ? next.slice(-MAX_ERRORS) : next;
}

const kindIcon: Record<ToastKind, IconName> = {
  success: "statusSucceeded",
  error: "statusFailed",
  info: "severityInfo",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(1);
  // Open modal dialogs, innermost last. While one is open the viewport renders
  // inside it: a modal makes the rest of the page inert, so toasts placed
  // outside would be unclickable and unannounced.
  const [dialogs, setDialogs] = useState<HTMLDialogElement[]>([]);
  useEffect(
    () =>
      subscribeModals(
        (dialog) => setDialogs((current) => [...current.filter((d) => d !== dialog), dialog]),
        (dialog) => setDialogs((current) => current.filter((d) => d !== dialog)),
      ),
    [],
  );
  const portalTarget = dialogs[dialogs.length - 1] ?? null;

  // The page behind a modal is inert, so a body live region written while a
  // dialog is open is never spoken. Holding the body announcer back until the
  // last dialog has closed makes "close the dialog, then toast" announce: the
  // text lands in an already-mounted, now-accessible region.
  //
  // A toast already spoken inside a dialog (the dialog stayed open) must not be
  // spoken again when the dialog closes, so those ids are left out of `delivered`.
  const [delivered, setDelivered] = useState<readonly ToastItem[]>([]);
  const [spoken, setSpoken] = useState<ReadonlySet<number>>(new Set());
  const markSpoken = useCallback((ids: readonly number[]) => {
    setSpoken((current) => (ids.every((id) => current.has(id)) ? current : new Set([...current, ...ids])));
  }, []);
  const deliverable = useMemo(() => items.filter((item) => !spoken.has(item.id)), [items, spoken]);
  const modalOpen = dialogs.length > 0;
  if (!modalOpen && delivered !== deliverable) {
    // Adjusting state during render (not in an effect) so the commit that
    // closes the last dialog already carries the text.
    setDelivered(deliverable);
  }
  const announcements: AnnouncementsValue = { live: items, delivered, markSpoken };

  const dismiss = useCallback((id: number) => {
    setItems((current) => current.filter((item) => item.id !== id));
  }, []);

  const show = useCallback<ShowToast>((toast) => {
    const item: ToastItem = { ...toast, kind: toast.kind ?? "info", id: nextId.current++ };
    setItems((current) => trim([...current, item]));
  }, []);

  // Visual stack only. Speech comes from the always-mounted announcers, so this
  // tree may move between the page and an open dialog without losing anything.
  const viewport = (
    <div className={styles.viewport}>
      <ul className={styles.list}>
        {items.map((item) => (
          <ToastView key={item.id} item={item} onDismiss={dismiss} />
        ))}
      </ul>
    </div>
  );

  return (
    <ToastContext.Provider value={show}>
      <AnnouncementsContext.Provider value={announcements}>
        {children}
        <ToastAnnouncer scope="page" />
        {portalTarget ? createPortal(viewport, portalTarget) : viewport}
      </AnnouncementsContext.Provider>
    </ToastContext.Provider>
  );
}

/**
 * Screen-reader output for toasts: a polite region for success/info and an
 * assertive one for errors, visually hidden. One lives on the page and one
 * inside every <Dialog> (rendered by Dialog). Both stay mounted for good; the
 * inert page and the hidden closed dialog keep the inactive one silent.
 */
export function ToastAnnouncer({ scope }: { scope: "page" | "dialog" }) {
  const value = useContext(AnnouncementsContext);
  const rootRef = useRef<HTMLDivElement>(null);
  const live = value?.live;
  const markSpoken = value?.markSpoken;

  // A toast counts as spoken here only if its dialog is still open one task
  // later. In the "close the dialog, then toast" flow the dialog is already
  // closed by then, so the page announcer still delivers that toast.
  useEffect(() => {
    if (scope !== "dialog" || !live || !markSpoken || live.length === 0) return;
    const timer = window.setTimeout(() => {
      if (rootRef.current?.closest("dialog")?.open) markSpoken(live.map((item) => item.id));
    }, 0);
    return () => window.clearTimeout(timer);
  }, [scope, live, markSpoken]);

  if (!value) return null;
  const source = scope === "page" ? value.delivered : value.live;
  const line = (item: ToastItem) => (
    <p key={item.id}>{item.description ? `${item.title}. ${item.description}` : item.title}</p>
  );
  return (
    <div ref={rootRef} className="sr-only">
      <div role="status" aria-live="polite">
        {source.filter((item) => item.kind !== "error").map(line)}
      </div>
      <div role="alert">{source.filter((item) => item.kind === "error").map(line)}</div>
    </div>
  );
}

function ToastView({ item, onDismiss }: { item: ToastItem; onDismiss: (id: number) => void }) {
  const { t } = useI18n();
  const [paused, setPaused] = useState(false);

  // Errors stay until dismissed: they usually need to be read and acted on.
  useEffect(() => {
    if (item.kind === "error" || paused) return;
    const timer = window.setTimeout(() => onDismiss(item.id), AUTO_DISMISS_MS);
    return () => window.clearTimeout(timer);
  }, [item.id, item.kind, paused, onDismiss]);

  return (
    <li
      className={cx(styles.toast, styles[item.kind])}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
    >
      <Icon name={kindIcon[item.kind]} size={18} className={styles.icon} />
      <div className={styles.text}>
        <p className={styles.title}>{item.title}</p>
        {item.description ? <p className={styles.description}>{item.description}</p> : null}
      </div>
      <button type="button" className={styles.dismiss} onClick={() => onDismiss(item.id)} aria-label={t("common.dismiss")}>
        <Icon name="close" />
      </button>
    </li>
  );
}

export function useToast(): ShowToast {
  const show = useContext(ToastContext);
  if (!show) throw new Error("useToast must be used inside <ToastProvider>");
  return show;
}
