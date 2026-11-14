// SPDX-License-Identifier: GPL-3.0-or-later
import { useCallback, useEffect, useRef, useState, type ChangeEvent } from "react";
import {
  MAX_IMPORT_BYTES,
  RepoError,
  deleteRepo,
  describeRepoError,
  exportRepos,
  importRepos,
  listRepos,
  setRepoEnabled,
  toRepoError,
  type Repository,
} from "../../api/repos";
import { Button, Dialog, EmptyState, Icon, PageHeader, useToast } from "../../components";
import { useI18n } from "../../i18n";
import { ExportDialog } from "./ExportDialog";
import { RepoForm } from "./RepoForm";
import { RepoList } from "./RepoList";
import styles from "./Repos.module.css";

type FormTarget = { repo: Repository | null };

export function ReposScreen() {
  const { t } = useI18n();
  const toast = useToast();
  const [repos, setRepos] = useState<Repository[] | null>(null);
  const [loadError, setLoadError] = useState<RepoError | null>(null);
  const [form, setForm] = useState<FormTarget | null>(null);
  const [removing, setRemoving] = useState<Repository | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const [togglingIds, setTogglingIds] = useState<ReadonlySet<number>>(new Set());
  const [exportDoc, setExportDoc] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  /** Reloads the list after a change; what is on screen stays while it runs. */
  const refresh = useCallback(async () => {
    try {
      const next = await listRepos();
      if (alive.current) setRepos(next);
    } catch (error) {
      if (alive.current) {
        toast({ kind: "error", title: t("repos.toast.failed"), description: describeRepoError(toRepoError(error), t) });
      }
    }
  }, [t, toast]);

  // First load. A failure shows the error state with a retry button.
  const loadFirst = useCallback(() => {
    listRepos().then(
      (next) => {
        if (alive.current) setRepos(next);
      },
      (error: unknown) => {
        if (alive.current) setLoadError(toRepoError(error));
      },
    );
  }, []);

  useEffect(() => {
    loadFirst();
  }, [loadFirst]);

  const retry = () => {
    setLoadError(null);
    loadFirst();
  };

  const toggle = async (repo: Repository, enabled: boolean) => {
    const hold = (on: boolean) =>
      setTogglingIds((current) => {
        const next = new Set(current);
        if (on) next.add(repo.id);
        else next.delete(repo.id);
        return next;
      });
    hold(true);
    try {
      const saved = await setRepoEnabled(repo.id, enabled);
      if (alive.current) setRepos((current) => current?.map((item) => (item.id === saved.id ? saved : item)) ?? current);
    } catch (error) {
      if (!alive.current) return;
      const failure = toRepoError(error);
      toast({ kind: "error", title: t("repos.toast.failed"), description: describeRepoError(failure, t) });
      if (failure.code === "repo_not_found") void refresh();
    } finally {
      // Only this repo's own hold is released: other toggles may still be running.
      if (alive.current) hold(false);
    }
  };

  const confirmRemove = async () => {
    if (!removing) return;
    setRemoveBusy(true);
    setRemoveError(null);
    const target = removing;
    try {
      await deleteRepo(target.id);
      if (!alive.current) return;
      setRemoving(null);
      setRepos((current) => current?.filter((item) => item.id !== target.id) ?? current);
      toast({ kind: "success", title: t("repos.toast.removed", { repo: `${target.owner}/${target.name}` }) });
    } catch (error) {
      if (!alive.current) return;
      const failure = toRepoError(error);
      if (failure.code === "repo_not_found") {
        // Already gone (removed elsewhere): the list is simply out of date.
        setRemoving(null);
        void refresh();
      } else {
        setRemoveError(describeRepoError(failure, t));
      }
    } finally {
      if (alive.current) setRemoveBusy(false);
    }
  };

  const startExport = async () => {
    setExporting(true);
    try {
      const doc = await exportRepos();
      if (alive.current) setExportDoc(doc);
    } catch (error) {
      if (alive.current) {
        toast({ kind: "error", title: t("repos.export.failed"), description: describeRepoError(toRepoError(error), t) });
      }
    } finally {
      if (alive.current) setExporting(false);
    }
  };

  const onFileChosen = async (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.target;
    const file = input.files?.[0];
    // Reset so choosing the same file again still fires `change`.
    input.value = "";
    if (!file) return;
    setImporting(true);
    try {
      if (file.size > MAX_IMPORT_BYTES) throw new RepoError("repo_import_too_large");
      const result = await importRepos(await file.text());
      if (!alive.current) return;
      const parts = [t("repos.import.done", { created: result.created, skipped: result.skipped })];
      if (result.pathsDropped > 0) parts.push(t("repos.import.dropped", { dropped: result.pathsDropped }));
      toast({ kind: "success", title: t("repos.import.title"), description: parts.join(" ") });
      void refresh();
    } catch (error) {
      if (alive.current) {
        toast({ kind: "error", title: t("repos.import.failed"), description: describeRepoError(toRepoError(error), t) });
      }
    } finally {
      if (alive.current) setImporting(false);
    }
  };

  const importButton = (
    <Button icon="upload" loading={importing} onClick={() => fileInput.current?.click()}>
      {t("repos.import")}
    </Button>
  );

  // An empty list offers the same actions in its empty state, so the header stays quiet.
  const isEmpty = repos !== null && repos.length === 0;
  const actions = isEmpty ? null : (
    <>
      <Button variant="primary" icon="plus" onClick={() => setForm({ repo: null })}>
        {t("repos.add")}
      </Button>
      {importButton}
      <Button icon="download" loading={exporting} disabled={repos === null} onClick={() => void startExport()}>
        {t("repos.export")}
      </Button>
    </>
  );

  let body;
  if (loadError) {
    body = (
      <div className={styles.noticeError} role="alert">
        <span className={styles.noticeTitle}>
          <Icon name="statusFailed" size={16} />
          {t("repos.loadError.title")}
        </span>
        <span>{describeRepoError(loadError, t)}</span>
        <div className={styles.noticeAction}>
          <Button onClick={retry}>{t("repos.retry")}</Button>
        </div>
      </div>
    );
  } else if (repos === null) {
    body = (
      <p className={styles.meta} role="status">
        {t("common.loading")}
      </p>
    );
  } else if (isEmpty) {
    body = (
      <EmptyState
        icon="repos"
        title={t("screen.repos.empty.title")}
        description={t("screen.repos.empty.description")}
        action={
          <div className={styles.emptyActions}>
            <Button variant="primary" icon="plus" onClick={() => setForm({ repo: null })}>
              {t("repos.add")}
            </Button>
            {importButton}
          </div>
        }
      />
    );
  } else {
    body = (
      <RepoList
        repos={repos}
        busyIds={togglingIds}
        onToggle={(repo, enabled) => void toggle(repo, enabled)}
        onEdit={(repo) => setForm({ repo })}
        onRemove={(repo) => {
          setRemoveError(null);
          setRemoving(repo);
        }}
      />
    );
  }

  const removingSlug = removing ? `${removing.owner}/${removing.name}` : "";

  return (
    <>
      <PageHeader title={t("screen.repos.title")} description={t("screen.repos.description")} actions={actions} />
      {/* Opened by the Import buttons; the visible control is the button, not this input. */}
      <input
        ref={fileInput}
        type="file"
        accept="application/json,.json"
        hidden
        tabIndex={-1}
        aria-hidden="true"
        onChange={(event) => void onFileChosen(event)}
      />
      {body}

      {form ? (
        <RepoForm
          repo={form.repo}
          onClose={() => setForm(null)}
          onSaved={(saved, created) => {
            setForm(null);
            toast({
              kind: "success",
              title: t(created ? "repos.toast.added" : "repos.toast.updated", { repo: `${saved.owner}/${saved.name}` }),
            });
            void refresh();
          }}
        />
      ) : null}

      <Dialog
        open={removing !== null}
        onClose={() => {
          if (!removeBusy) setRemoving(null);
        }}
        title={t("repos.remove.title", { repo: removingSlug })}
        description={t("repos.remove.description")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setRemoving(null)} disabled={removeBusy}>
              {t("repos.cancel")}
            </Button>
            <Button variant="danger" icon="trash" loading={removeBusy} onClick={() => void confirmRemove()}>
              {t("repos.remove")}
            </Button>
          </>
        }
      >
        {removeError ? (
          <div className={styles.noticeError} role="alert">
            <span className={styles.noticeTitle}>
              <Icon name="statusFailed" size={16} />
              {t("repos.remove.error.title")}
            </span>
            {removeError}
          </div>
        ) : null}
      </Dialog>

      {exportDoc !== null ? <ExportDialog document={exportDoc} onClose={() => setExportDoc(null)} /> : null}
    </>
  );
}
