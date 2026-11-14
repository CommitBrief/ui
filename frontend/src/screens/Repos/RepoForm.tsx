// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  addRepo,
  describeRepoError,
  errorFormField,
  pickFolder,
  suggestFromLocalPath,
  toRepoError,
  updateRepo,
  type RemoteSuggestion,
  type Repository,
} from "../../api/repos";
import { Button, Dialog, Icon, Input } from "../../components";
import { useI18n } from "../../i18n";
import { joinSlug, sameSlug, splitSlug, validateDraft } from "./validation";
import styles from "./Repos.module.css";

export interface RepoFormProps {
  /** The repo being edited; null adds a new one. */
  repo: Repository | null;
  onClose: () => void;
  /** Called after the backend accepted the repo. The owner closes the form. */
  onSaved: (saved: Repository, created: boolean) => void;
}

const FORM_ID = "repo-form";

/**
 * Add and edit form in a dialog. The screen mounts it only while it is needed,
 * so every opening starts from clean state.
 *
 * The local path comes from the native folder chooser (an app service method),
 * never from typing; choosing one asks the backend for the origin remote and
 * fills the owner/name input from it.
 */
export function RepoForm({ repo, onClose, onSaved }: RepoFormProps) {
  const { t } = useI18n();
  const [slug, setSlug] = useState(repo ? joinSlug(repo.owner, repo.name) : "");
  const [localPath, setLocalPath] = useState(repo?.localPath ?? "");
  // The owner/name input counts as the user's once they typed in it (or it holds
  // an existing repo); a later suggestion then asks instead of overwriting.
  const [slugTouched, setSlugTouched] = useState(repo !== null);
  const [suggestion, setSuggestion] = useState<RemoteSuggestion | null>(null);
  const [slugError, setSlugError] = useState<string | null>(null);
  const [pathError, setPathError] = useState<string | null>(null);
  // A path that was accepted but gave no name (no origin, not GitHub).
  const [pathNotice, setPathNotice] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [picking, setPicking] = useState(false);
  const [saving, setSaving] = useState(false);
  const slugRef = useRef<HTMLInputElement>(null);

  // Guards setState after the dialog went away while a call was in flight.
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  // A save in flight cannot be walked away from: the backend finishes it either
  // way, and a closed form would never tell the list about it.
  const close = () => {
    if (!saving) onClose();
  };

  const editing = repo !== null;
  const suggestedSlug = suggestion ? joinSlug(suggestion.owner, suggestion.name) : null;
  const ownSlug = repo ? joinSlug(repo.owner, repo.name) : null;
  // "Already in the list" is only news when it is not the repo being edited.
  const suggestionIsDuplicate = suggestion?.alreadyAdded === true && !(ownSlug && suggestedSlug && sameSlug(ownSlug, suggestedSlug));
  const suggestionDiffers = suggestedSlug !== null && !sameSlug(suggestedSlug, slug);

  const choose = async () => {
    setPicking(true);
    setPathError(null);
    setPathNotice(null);
    setFormError(null);
    try {
      const picked = await pickFolder(t("repos.picker.title"));
      if (!alive.current || picked === "") return; // canceled
      try {
        const found = await suggestFromLocalPath(picked);
        if (!alive.current) return;
        setLocalPath(picked);
        setSuggestion(found);
        if (!slugTouched || slug.trim() === "") {
          setSlug(joinSlug(found.owner, found.name));
          setSlugError(null);
        }
      } catch (error) {
        if (!alive.current) return;
        const failure = toRepoError(error);
        if (failure.code === "repo_no_remote" || failure.code === "repo_remote_unsupported") {
          // The folder is fine, it just cannot name the repo: keep it, say why.
          setLocalPath(picked);
          setSuggestion(null);
          setPathNotice(describeRepoError(failure, t));
        } else {
          setPathError(describeRepoError(failure, t));
        }
      }
    } catch (error) {
      if (alive.current) setFormError(describeRepoError(toRepoError(error), t));
    } finally {
      if (alive.current) setPicking(false);
    }
  };

  const clearPath = () => {
    setLocalPath("");
    setSuggestion(null);
    setPathError(null);
    setPathNotice(null);
  };

  const useSuggestion = () => {
    if (suggestedSlug === null) return;
    setSlug(suggestedSlug);
    setSlugTouched(true);
    setSlugError(null);
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving) return;
    setFormError(null);
    setPathError(null);
    const errors = validateDraft({ slug, localPath });
    if (errors.slug) {
      setSlugError(t(errors.slug));
      slugRef.current?.focus();
      return;
    }
    setSlugError(null);
    const parsed = splitSlug(slug);
    if (parsed === null) return; // unreachable: validateDraft accepted the slug
    const input = { owner: parsed.owner, name: parsed.name, localPath };
    setSaving(true);
    try {
      const saved = repo ? await updateRepo(repo.id, input) : await addRepo(input);
      if (alive.current) onSaved(saved, repo === null);
    } catch (error) {
      if (!alive.current) return;
      const failure = toRepoError(error);
      const message = describeRepoError(failure, t);
      switch (errorFormField(failure)) {
        case "slug":
          setSlugError(message);
          slugRef.current?.focus();
          break;
        case "localPath":
          setPathError(message);
          break;
        default:
          setFormError(message);
      }
    } finally {
      if (alive.current) setSaving(false);
    }
  };

  return (
    <Dialog
      open
      onClose={close}
      title={t(editing ? "repos.form.edit.title" : "repos.form.add.title")}
      description={t(editing ? "repos.form.edit.description" : "repos.form.add.description")}
      footer={
        <>
          <Button variant="ghost" onClick={close} disabled={saving}>
            {t("repos.cancel")}
          </Button>
          <Button type="submit" form={FORM_ID} variant="primary" loading={saving}>
            {t(editing ? "repos.form.save.edit" : "repos.form.save.add")}
          </Button>
        </>
      }
    >
      <form id={FORM_ID} className={styles.form} onSubmit={submit} noValidate>
        {formError ? (
          <div className={styles.noticeError} role="alert">
            <span className={styles.noticeTitle}>
              <Icon name="statusFailed" size={16} />
              {t("repos.form.error.title")}
            </span>
            {formError}
          </div>
        ) : null}

        <div className={styles.pathField}>
          <Input
            label={t("repos.form.path.label")}
            hint={t("repos.form.path.hint")}
            error={pathError ?? undefined}
            value={localPath}
            placeholder={t("repos.form.path.empty")}
            readOnly
            spellCheck={false}
            className={styles.pathInput}
          />
          <div className={styles.pathActions}>
            <Button icon="folder" loading={picking} onClick={choose}>
              {t(localPath === "" ? "repos.form.path.choose" : "repos.form.path.change")}
            </Button>
            {localPath !== "" ? (
              <Button variant="ghost" onClick={clearPath}>
                {t("repos.form.path.clear")}
              </Button>
            ) : null}
          </div>
        </div>

        {pathNotice ? (
          <p className={styles.notice} role="status">
            <Icon name="severityInfo" size={16} className={styles.noticeIcon} />
            {pathNotice}
          </p>
        ) : null}
        {localPath === "" ? (
          <p className={styles.noticeWarning}>
            <Icon name="statusAttention" size={16} className={styles.noticeIcon} />
            {t("repos.noPath.warning")}
          </p>
        ) : null}

        <Input
          ref={slugRef}
          label={t("repos.form.slug.label")}
          hint={t("repos.form.slug.hint")}
          error={slugError ?? undefined}
          value={slug}
          onChange={(event) => {
            setSlug(event.target.value);
            setSlugTouched(true);
            setSlugError(null);
          }}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          required
        />
        {suggestionIsDuplicate && suggestedSlug ? (
          <p className={styles.noticeWarning} role="status">
            <Icon name="statusAttention" size={16} className={styles.noticeIcon} />
            {t("repos.form.suggest.added", { repo: suggestedSlug })}
          </p>
        ) : null}
        {suggestionDiffers && suggestedSlug ? (
          <p className={styles.notice} role="status">
            <Icon name="severityInfo" size={16} className={styles.noticeIcon} />
            <span>
              {t("repos.form.suggest.origin", { repo: suggestedSlug })}{" "}
              <button type="button" className={styles.linkButton} onClick={useSuggestion}>
                {t("repos.form.suggest.use")}
              </button>
            </span>
          </p>
        ) : null}
      </form>
    </Dialog>
  );
}
