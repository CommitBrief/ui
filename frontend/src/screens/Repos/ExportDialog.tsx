// SPDX-License-Identifier: GPL-3.0-or-later
import { useRef, useState } from "react";
import { describeRepoError, saveRepoExport, toRepoError } from "../../api/repos";
import { Button, Dialog, useToast } from "../../components";
import { useI18n } from "../../i18n";
import styles from "./Repos.module.css";

const FILE_NAME = "commitbrief-ui-repos.json";

export interface ExportDialogProps {
  /** The export document the backend rendered (shown for copying). */
  document: string;
  onClose: () => void;
}

/** The legacy copy command: the fallback where the async clipboard is missing. */
function execCopy(): boolean {
  try {
    return window.document.execCommand("copy");
  } catch {
    return false;
  }
}

/**
 * Shows the export document with two ways out: save it as a file or copy it.
 * Saving goes through the backend (native save dialog, the file is written on
 * the Go side): a webview does not turn a download link into a file.
 */
export function ExportDialog({ document: doc, onClose }: ExportDialogProps) {
  const { t } = useI18n();
  const toast = useToast();
  const area = useRef<HTMLTextAreaElement>(null);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      const file = await saveRepoExport(t("repos.export.saveTitle"), FILE_NAME);
      // "" is a canceled dialog: the user stays in this dialog.
      if (file === "") return;
      onClose();
      toast({ kind: "success", title: t("repos.export.saved", { file }) });
    } catch (error) {
      toast({ kind: "error", title: t("repos.export.failed"), description: describeRepoError(toRepoError(error), t) });
    } finally {
      setSaving(false);
    }
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(doc);
      toast({ kind: "success", title: t("repos.export.copied") });
      return;
    } catch {
      // Falls through: the async clipboard is not available in every webview.
    }
    const element = area.current;
    element?.focus();
    element?.select();
    toast(
      execCopy()
        ? { kind: "success", title: t("repos.export.copied") }
        : { kind: "error", title: t("repos.export.copyFailed") },
    );
  };

  return (
    <Dialog
      open
      onClose={() => {
        if (!saving) onClose();
      }}
      title={t("repos.export.title")}
      description={t("repos.export.description")}
      footer={
        <>
          <Button icon="copy" onClick={() => void copy()}>
            {t("repos.export.copy")}
          </Button>
          <Button variant="primary" icon="download" loading={saving} onClick={() => void save()}>
            {t("repos.export.save")}
          </Button>
        </>
      }
    >
      <textarea
        ref={area}
        className={styles.exportArea}
        aria-label={t("repos.export.label")}
        value={doc}
        readOnly
        rows={12}
        wrap="off"
        spellCheck={false}
      />
    </Dialog>
  );
}
