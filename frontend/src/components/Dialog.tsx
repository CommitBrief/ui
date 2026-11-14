// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useId, useRef, type ReactNode } from "react";
import { useI18n } from "../i18n";
import { Icon } from "./Icon";
import { announceModalClosed, announceModalOpened } from "./modalEvents";
import { ToastAnnouncer } from "./Toast";
import styles from "./Dialog.module.css";

export interface DialogProps {
  open: boolean;
  /** Called for Esc, the close button and a backdrop click. */
  onClose: () => void;
  title: string;
  description?: string;
  children?: ReactNode;
  /** Action row; put the primary action last. */
  footer?: ReactNode;
}

/**
 * Built on the native <dialog> opened with showModal(): the platform provides
 * the focus trap, inert background, Esc handling and focus return to the opener.
 */
export function Dialog({ open, onClose, title, description, children, footer }: DialogProps) {
  const { t } = useI18n();
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const descriptionId = useId();

  // The native `close` event can arrive without a preceding `cancel` (for
  // example Esc in WebView2 without user activation). If React still thinks the
  // dialog is open at that point, tell the owner, so state and element agree.
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);

  // The cleanup closes the element, so every way of setting `open` to false (or
  // unmounting while open) goes through the same path.
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog || !open) return;
    if (!dialog.open) dialog.showModal();
    announceModalOpened(dialog);
    return () => {
      announceModalClosed(dialog);
      if (dialog.open) dialog.close();
    };
  }, [open]);

  return (
    <dialog
      ref={ref}
      className={styles.dialog}
      aria-labelledby={titleId}
      aria-describedby={description ? descriptionId : undefined}
      onClose={(event) => {
        // A `close` event is queued by close() and fires later. If the dialog
        // was opened again in between (React StrictMode runs an effect's
        // cleanup and setup back to back on a dialog that mounts already open),
        // the event is stale: the dialog is open, so nothing has closed.
        if (event.currentTarget.open) return;
        announceModalClosed(event.currentTarget); // idempotent; frees the toast viewport
        if (openRef.current) onClose();
      }}
      onCancel={(event) => {
        // Let React state own the open flag; the effect closes the element.
        event.preventDefault();
        onClose();
      }}
      onMouseDown={(event) => {
        // The <dialog> element itself is the backdrop area; content sits in a child.
        if (event.target === event.currentTarget) onClose();
      }}
    >
      {/* Always mounted (even closed) so its live regions exist before a toast arrives. */}
      <ToastAnnouncer scope="dialog" />
      {open ? (
        <div className={styles.surface}>
          <header className={styles.header}>
            <div className={styles.heading}>
              <h2 id={titleId} className={styles.title}>
                {title}
              </h2>
              {description ? (
                <p id={descriptionId} className={styles.description}>
                  {description}
                </p>
              ) : null}
            </div>
            <button type="button" className={styles.close} onClick={onClose} aria-label={t("common.close")}>
              <Icon name="close" />
            </button>
          </header>
          {children ? <div className={styles.body}>{children}</div> : null}
          {footer ? <footer className={styles.footer}>{footer}</footer> : null}
        </div>
      ) : null}
    </dialog>
  );
}
