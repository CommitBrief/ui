// SPDX-License-Identifier: GPL-3.0-or-later
import { useState } from "react";
import { Button, Dialog, Icon } from "../../components";
import { useI18n, type MessageKey } from "../../i18n";
import styles from "./Schedules.module.css";

export interface PostConsentDialogProps {
  /** The user ticked the box and chose "Allow posting". */
  onConfirm: () => void;
  /** Esc, the close button, a backdrop click or "Keep preview": the mode goes back to preview. */
  onCancel: () => void;
}

const items: readonly MessageKey[] = [
  "schedules.consent.item.identity",
  "schedules.consent.item.requested",
  "schedules.consent.item.verdict",
  "schedules.consent.item.revert",
];

/**
 * The explicit consent for post mode (UI-0008 section 2): the review is written
 * under the user's own GitHub identity, GitHub drops them from the requested
 * reviewers, and the verdict is capped at `comment`. "Allow posting" stays
 * disabled until the box is ticked. The owner mounts it only while it is needed,
 * so every opening starts unticked.
 */
export function PostConsentDialog({ onConfirm, onCancel }: PostConsentDialogProps) {
  const { t } = useI18n();
  const [understood, setUnderstood] = useState(false);

  return (
    <Dialog
      open
      onClose={onCancel}
      title={t("schedules.consent.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onCancel}>
            {t("schedules.consent.cancel")}
          </Button>
          <Button variant="primary" icon="send" disabled={!understood} onClick={onConfirm}>
            {t("schedules.consent.confirm")}
          </Button>
        </>
      }
    >
      <div className={styles.form}>
        <p>{t("schedules.consent.intro")}</p>
        <ul className={styles.consentList}>
          {items.map((key) => (
            <li key={key} className={styles.consentItem}>
              <Icon name="statusAttention" size={16} className={styles.consentIcon} />
              <span>{t(key)}</span>
            </li>
          ))}
        </ul>
        <label className={styles.consentCheck}>
          <input type="checkbox" checked={understood} onChange={(event) => setUnderstood(event.target.checked)} />
          <span>{t("schedules.consent.check")}</span>
        </label>
      </div>
    </Dialog>
  );
}
