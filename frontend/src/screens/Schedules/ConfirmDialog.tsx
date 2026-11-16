// SPDX-License-Identifier: GPL-3.0-or-later
import type { ReactNode } from "react";
import { Button, Dialog, type ButtonVariant } from "../../components";
import type { IconName } from "../../components";
import { useI18n } from "../../i18n";
import { Notice } from "../Jobs/parts";

export interface ConfirmDialogProps {
  title: string;
  description: string;
  confirmLabel: string;
  confirmIcon?: IconName;
  confirmVariant?: ButtonVariant;
  /** The call is in flight: it cannot be walked away from. */
  busy: boolean;
  /** The failure of the last attempt, in words. */
  error: string | null;
  onConfirm: () => void;
  onClose: () => void;
  children?: ReactNode;
}

/** A plain yes/no question before something that cannot be undone or that posts to GitHub. */
export function ConfirmDialog({ title, description, confirmLabel, confirmIcon, confirmVariant = "primary", busy, error, onConfirm, onClose, children }: ConfirmDialogProps) {
  const { t } = useI18n();
  const close = () => {
    if (!busy) onClose();
  };
  return (
    <Dialog
      open
      onClose={close}
      title={title}
      description={description}
      footer={
        <>
          <Button variant="ghost" onClick={close} disabled={busy}>
            {t("schedules.cancel")}
          </Button>
          <Button variant={confirmVariant} icon={confirmIcon} loading={busy} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      {error ? (
        <Notice tone="error" live="alert">
          {error}
        </Notice>
      ) : null}
      {children}
    </Dialog>
  );
}
