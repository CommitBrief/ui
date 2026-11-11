// SPDX-License-Identifier: GPL-3.0-or-later

// A modal <dialog> opened with showModal() makes everything outside its subtree
// inert and unclickable, including anything in the top layer. Toasts raised while
// a dialog is open would therefore be unreachable. Dialog announces itself with
// these events so ToastProvider can render its viewport inside the open dialog.

const MODAL_OPENED = "commitbrief:modal-opened";
const MODAL_CLOSED = "commitbrief:modal-closed";

export function announceModalOpened(dialog: HTMLDialogElement): void {
  window.dispatchEvent(new CustomEvent(MODAL_OPENED, { detail: dialog }));
}

export function announceModalClosed(dialog: HTMLDialogElement): void {
  window.dispatchEvent(new CustomEvent(MODAL_CLOSED, { detail: dialog }));
}

/** Subscribes to open/close announcements; returns the unsubscribe function. */
export function subscribeModals(
  onOpened: (dialog: HTMLDialogElement) => void,
  onClosed: (dialog: HTMLDialogElement) => void,
): () => void {
  const opened = (event: Event) => onOpened((event as CustomEvent<HTMLDialogElement>).detail);
  const closed = (event: Event) => onClosed((event as CustomEvent<HTMLDialogElement>).detail);
  window.addEventListener(MODAL_OPENED, opened);
  window.addEventListener(MODAL_CLOSED, closed);
  return () => {
    window.removeEventListener(MODAL_OPENED, opened);
    window.removeEventListener(MODAL_CLOSED, closed);
  };
}
