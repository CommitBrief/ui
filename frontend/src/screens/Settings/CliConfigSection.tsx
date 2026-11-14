// SPDX-License-Identifier: GPL-3.0-or-later
import { useEffect, useMemo, useRef, useState } from "react";
import { listRepos, type Repository } from "../../api/repos";
import {
  configFiles,
  describeSettingsError,
  listConfig,
  openConfigFile,
  setConfig,
  toSettingsError,
  USER_SCOPE,
  type CLIStatus,
  type ConfigEntry,
  type ConfigFiles,
} from "../../api/settings";
import type { Translate } from "../../api/settingsErrors";
import { Button, Input, Select, Switch, useToast } from "../../components";
import { useI18n } from "../../i18n";
import {
  groupEntries,
  groupTitleKeys,
  isDirty,
  keyHintKeys,
  omitKey,
  shownValue,
  validateDraft,
  valueToWrite,
  type Drafts,
  type FieldIssue,
} from "./configFields";
import { CopyCommand, LoadFailed, Loading, Notice, Section } from "./parts";
import { useLoad, type Loader } from "./useLoad";
import { useWriteLock } from "./WriteLockContext";
import styles from "./Settings.module.css";

/** CLI configuration comes from `commitbrief config get`, so nothing loads until the CLI is known to be usable. */
export function CliConfigSection({ cli }: { cli: Loader<CLIStatus> }) {
  const { t } = useI18n();
  const status = cli.data;
  let body;
  if (!status) {
    body = cli.error ? <LoadFailed error={cli.error} onRetry={cli.reload} /> : <Loading />;
  } else if (status.state !== "ok") {
    body = <Notice tone="warning">{t("settings.config.cliNeeded")}</Notice>;
  } else {
    body = <ConfigBody key={status.version} />;
  }
  return (
    <Section anchor="settings-config" title={t("settings.config.title")} description={t("settings.config.description")}>
      {body}
    </Section>
  );
}

interface FilesState {
  repoId: number;
  files: ConfigFiles | null;
}

function ConfigBody() {
  const { t } = useI18n();
  const toast = useToast();
  const config = useLoad(listConfig);
  const write = useWriteLock();
  const repos = useLoad(listRepos);
  const [scopeRepoId, setScopeRepoId] = useState<number>(USER_SCOPE);
  const [draftsByScope, setDraftsByScope] = useState<Readonly<Record<number, Drafts>>>({});
  const [errorsByScope, setErrorsByScope] = useState<Readonly<Record<number, Readonly<Record<string, string>>>>>({});
  // Values written to a repository's own file during this session: the CLI cannot read one file alone.
  const [writtenByScope, setWrittenByScope] = useState<Readonly<Record<number, Readonly<Record<string, string>>>>>({});
  const [saving, setSaving] = useState(false);
  const [filesState, setFilesState] = useState<FilesState | null>(null);
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  // Where the config file of the chosen scope is (for the hint and the "edit in the file" keys).
  useEffect(() => {
    let live = true;
    configFiles(scopeRepoId).then(
      (files) => {
        if (live) setFilesState({ repoId: scopeRepoId, files });
      },
      () => {
        if (live) setFilesState({ repoId: scopeRepoId, files: null });
      },
    );
    return () => {
      live = false;
    };
  }, [scopeRepoId]);

  const repoScope = scopeRepoId !== USER_SCOPE;
  const drafts = draftsByScope[scopeRepoId] ?? {};
  const errors = errorsByScope[scopeRepoId] ?? {};
  const written = writtenByScope[scopeRepoId] ?? {};
  const files = filesState?.repoId === scopeRepoId ? filesState.files : null;
  const configPath = repoScope ? files?.repoPath : files?.userPath;

  const entries = config.data;
  const groups = useMemo(() => (entries ? groupEntries(entries) : []), [entries]);

  const reposWithPath: Repository[] = (repos.data ?? []).filter((repo) => repo.localPath !== "");
  const someWithoutPath = (repos.data ?? []).some((repo) => repo.localPath === "");

  if (!entries) return config.error ? <LoadFailed error={config.error} onRetry={config.reload} /> : <Loading />;

  const setDraft = (key: string, value: string) => {
    setDraftsByScope((current) => ({ ...current, [scopeRepoId]: { ...(current[scopeRepoId] ?? {}), [key]: value } }));
    setErrorsByScope((current) => {
      const mine = current[scopeRepoId];
      if (!mine || !(key in mine)) return current;
      return { ...current, [scopeRepoId]: omitKey(mine, key) };
    });
  };

  const dirtyEntries = entries.filter((entry) => isDirty(entry, drafts, repoScope));
  const issues = new Map<string, FieldIssue>();
  for (const entry of dirtyEntries) {
    const issue = validateDraft(entry.field, drafts[entry.field.key] ?? "", repoScope);
    if (issue) issues.set(entry.field.key, issue);
  }

  const discard = () => {
    setDraftsByScope((current) => ({ ...current, [scopeRepoId]: {} }));
    setErrorsByScope((current) => ({ ...current, [scopeRepoId]: {} }));
  };

  const save = async () => {
    if (saving || dirtyEntries.length === 0 || issues.size > 0) return;
    const release = write.begin();
    if (!release) return;
    const scope = scopeRepoId;
    setSaving(true);
    let ok = 0;
    const succeeded: string[] = [];
    const failures: Record<string, string> = {};
    const nowWritten: Record<string, string> = {};
    for (const entry of dirtyEntries) {
      const key = entry.field.key;
      const value = valueToWrite(drafts[key] ?? "");
      try {
        await setConfig(key, value, scope);
        ok++;
        succeeded.push(key);
        nowWritten[key] = value;
      } catch (error) {
        const failure = toSettingsError(error);
        failures[key] =
          failure.code === "settings_value_invalid"
            ? t("settings.config.error.invalid")
            : describeSettingsError(failure, t);
      }
    }
    release();
    if (!alive.current) return;
    setSaving(false);
    setDraftsByScope((current) => ({
      ...current,
      [scope]: Object.fromEntries(Object.entries(current[scope] ?? {}).filter(([name]) => !succeeded.includes(name))),
    }));
    setErrorsByScope((current) => ({ ...current, [scope]: failures }));
    if (scope !== USER_SCOPE && ok > 0) {
      setWrittenByScope((current) => ({ ...current, [scope]: { ...(current[scope] ?? {}), ...nowWritten } }));
    }
    if (ok > 0) {
      toast({ kind: "success", title: t("settings.config.saved", { count: ok }) });
      // In the user scope the written values are what the CLI now reports; show them
      // at once, then confirm with a re-read (an environment variable may still win).
      if (scope === USER_SCOPE) {
        config.setData(
          entries.map((entry) => {
            const value = nowWritten[entry.field.key];
            return value === undefined ? entry : { ...entry, value };
          }),
        );
      }
      config.reload();
    }
    if (Object.keys(failures).length > 0) {
      toast({
        kind: "error",
        title: t("settings.config.saveFailed"),
        description: t("settings.config.saveFailedDescription"),
      });
    }
  };

  const scopeOptions = [
    { value: String(USER_SCOPE), label: t("settings.config.scope.user") },
    ...reposWithPath.map((repo) => ({ value: String(repo.id), label: `${repo.owner}/${repo.name}` })),
  ];

  const scopeHint = repoScope
    ? t("settings.config.scope.hint.repo", { path: configPath ?? ".commitbrief/config.yml" })
    : configPath
      ? t("settings.config.scope.hint.user", { path: configPath })
      : t("settings.config.scope.hint.userNoPath");

  return (
    <>
      {config.error ? <LoadFailed error={config.error} onRetry={config.reload} /> : null}

      <div className={styles.scopeBar}>
        <Select
          label={t("settings.config.scope.label")}
          hint={scopeHint}
          value={String(scopeRepoId)}
          disabled={saving}
          onChange={(event) => setScopeRepoId(Number(event.target.value))}
          options={scopeOptions}
        />
        {repos.error ? (
          <p className={styles.muted}>{t("settings.config.scope.reposFailed")}</p>
        ) : someWithoutPath ? (
          <p className={styles.muted}>{t("settings.config.scope.noPath")}</p>
        ) : null}
      </div>

      <div className={styles.groups}>
        {groups.map(({ group, entries: inGroup }) => {
          const editable = inGroup.filter((entry) => !entry.field.fileOnly);
          const fileOnly = inGroup.filter((entry) => entry.field.fileOnly);
          return (
            <fieldset key={group} className={styles.group}>
              <legend className={styles.groupTitle}>{t(groupTitleKeys[group])}</legend>
              {editable.length > 0 ? (
                <div className={styles.fields}>
                  {editable.map((entry) => (
                    <ConfigFieldControl
                      key={entry.field.key}
                      entry={entry}
                      value={shownValue(entry, drafts, repoScope)}
                      repoScope={repoScope}
                      disabled={saving}
                      error={errors[entry.field.key] ?? issueMessage(issues.get(entry.field.key), t)}
                      writtenValue={written[entry.field.key]}
                      onChange={(value) => setDraft(entry.field.key, value)}
                    />
                  ))}
                </div>
              ) : null}
              {fileOnly.map((entry) => (
                <FileOnlyKey key={entry.field.key} entry={entry} path={configPath ?? null} repoId={scopeRepoId} inherited={repoScope} />
              ))}
            </fieldset>
          );
        })}
      </div>

      <div className={styles.saveBar}>
        <Button
          variant="primary"
          loading={saving}
          disabled={dirtyEntries.length === 0 || issues.size > 0 || (write.busy && !saving)}
          onClick={() => void save()}
        >
          {dirtyEntries.length > 0
            ? t("settings.config.saveCount", { count: dirtyEntries.length })
            : t("settings.config.save")}
        </Button>
        <Button variant="ghost" disabled={saving || dirtyEntries.length === 0} onClick={discard}>
          {t("settings.config.discard")}
        </Button>
      </div>
    </>
  );
}

function issueMessage(issue: FieldIssue | undefined, t: Translate): string | undefined {
  return issue ? t(issue.key, issue.params) : undefined;
}

interface ControlProps {
  entry: ConfigEntry;
  value: string;
  repoScope: boolean;
  disabled: boolean;
  error?: string;
  writtenValue?: string;
  onChange: (value: string) => void;
}

/** One key: a switch, a select or a text field, with its one-line explanation and, in a repository scope, what it inherits. */
function ConfigFieldControl({ entry, value, repoScope, disabled, error, writtenValue, onChange }: ControlProps) {
  const { t } = useI18n();
  const { field } = entry;
  const hintKey = keyHintKeys[field.key];
  const parts: string[] = [];
  if (hintKey) parts.push(t(hintKey));
  if (repoScope) {
    parts.push(entry.value === "" ? t("settings.config.inheritedEmpty") : t("settings.config.inherited", { value: entry.value }));
    if (writtenValue !== undefined) parts.push(t("settings.config.repoSet", { value: writtenValue === "" ? "—" : writtenValue }));
  }
  const hint = parts.join(" ");

  if (entry.error !== "") {
    return (
      <Input
        label={field.key}
        value=""
        disabled
        readOnly
        error={t("settings.config.unreadable", { message: describeSettingsError({ code: entry.error }, t) })}
      />
    );
  }

  if (field.kind === "bool" && !repoScope) {
    return (
      <Switch
        label={field.key}
        description={hint}
        checked={value === "true"}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked ? "true" : "false")}
      />
    );
  }

  if (field.kind === "bool" || field.kind === "enum") {
    const choices =
      field.kind === "bool"
        ? [
            { value: "true", label: t("settings.config.on") },
            { value: "false", label: t("settings.config.off") },
          ]
        : (field.enum ?? []).map((choice) => ({ value: choice, label: choice }));
    // A repository scope starts on "inherit"; the user scope never offers it.
    const options = repoScope ? [{ value: "", label: t("settings.config.inherit") }, ...choices] : choices;
    return (
      <Select
        label={field.key}
        hint={hint || undefined}
        error={error}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        options={options}
      />
    );
  }

  const numeric = field.kind === "int" || field.kind === "float";
  return (
    <Input
      label={field.key}
      hint={hint || undefined}
      error={error}
      value={value}
      disabled={disabled}
      inputMode={numeric ? (field.kind === "int" ? "numeric" : "decimal") : undefined}
      autoComplete="off"
      autoCapitalize="off"
      spellCheck={false}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

/** A list key: shown, never editable here. The way to change it is the config file. */
function FileOnlyKey({ entry, path, repoId, inherited }: { entry: ConfigEntry; path: string | null; repoId: number; inherited: boolean }) {
  const { t } = useI18n();
  const toast = useToast();
  const [opening, setOpening] = useState(false);
  const hintKey = keyHintKeys[entry.field.key];

  // Only the scope goes to the backend; it works out the file itself.
  const open = async () => {
    setOpening(true);
    try {
      await openConfigFile(repoId);
    } catch (error) {
      toast({
        kind: "error",
        title: t("settings.config.fileOnly.openFailed"),
        description: describeSettingsError(toSettingsError(error), t),
      });
    } finally {
      setOpening(false);
    }
  };
  return (
    <div className={styles.fileOnly}>
      <p className={styles.fileOnlyKey}>
        <span className={styles.mono}>{entry.field.key}</span>
      </p>
      {hintKey ? <p className={styles.muted}>{t(hintKey)}</p> : null}
      <p className={styles.muted}>
        {/* In a repository scope the CLI reports the merged value (user file, environment, defaults): it is what the repository inherits, not what its own file holds. */}
        {inherited ? t("settings.config.fileOnly.inherited") : t("settings.config.fileOnly.value")}:{" "}
        <span className={styles.mono}>{entry.value === "" ? t("settings.config.fileOnly.none") : entry.value}</span>
      </p>
      <p>
        <strong>{t("settings.config.fileOnly")}.</strong> {t("settings.config.fileOnly.description")}
      </p>
      {path ? (
        <CopyCommand text={path} label={t("settings.config.fileOnly")} copyLabel={t("settings.config.fileOnly.copyPath")} />
      ) : (
        <p className={styles.muted}>{t("settings.config.fileOnly.pathUnknown")}</p>
      )}
      <div>
        <Button icon="pencil" loading={opening} disabled={!path} onClick={() => void open()}>
          {t("settings.config.fileOnly.open")}
        </Button>
      </div>
    </div>
  );
}
