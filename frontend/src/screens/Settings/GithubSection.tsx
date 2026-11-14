// SPDX-License-Identifier: GPL-3.0-or-later
import { githubStatus } from "../../api/settings";
import { Button } from "../../components";
import { useI18n } from "../../i18n";
import { describeAuth, loginCommand } from "./githubAuth";
import { CopyCommand, Facts, LoadFailed, Loading, Notice, Pill, Section } from "./parts";
import { useLoad } from "./useLoad";
import styles from "./Settings.module.css";

export function GithubSection() {
  const { t } = useI18n();
  const gh = useLoad(githubStatus);
  const status = gh.data;

  const refresh = (
    <Button icon="updates" loading={gh.loading && status !== null} onClick={gh.reload}>
      {t("settings.refresh")}
    </Button>
  );

  let body;
  if (!status) {
    body = gh.error ? <LoadFailed error={gh.error} onRetry={gh.reload} /> : <Loading />;
  } else {
    const view = describeAuth(status);
    const host = status.host || "github.com";
    const rows = [
      {
        label: t("settings.github.status.label"),
        value: <Pill tone={view.tone}>{t(view.label)}</Pill>,
      },
      ...(status.installed
        ? [{ label: t("settings.github.path"), value: <span className={styles.mono}>{status.path}</span> }]
        : []),
      ...(status.authenticated
        ? [
            { label: t("settings.github.account"), value: <span className={styles.mono}>{status.account}</span> },
            { label: t("settings.github.host"), value: <span className={styles.mono}>{host}</span> },
            {
              label: t("settings.github.scopes"),
              value:
                status.scopes && status.scopes.length > 0 ? (
                  <span className={styles.mono}>{status.scopes.join(", ")}</span>
                ) : (
                  t("settings.github.scopes.none")
                ),
            },
          ]
        : []),
    ];
    body = (
      <>
        {gh.error ? <LoadFailed error={gh.error} onRetry={gh.reload} /> : null}
        <Facts rows={rows} />
        {view.guide ? (
          <Notice tone={status.installed ? "warning" : "error"} title={t(status.installed ? "settings.github.login.title" : "settings.github.notInstalled.title")}>
            <p>{t(view.guide, { host })}</p>
            {view.showCommand ? <CopyCommand text={loginCommand(host)} label={t("settings.github.command.label")} /> : null}
          </Notice>
        ) : null}
      </>
    );
  }

  return (
    <Section anchor="settings-github" title={t("settings.github.title")} description={t("settings.github.description")} actions={refresh}>
      {body}
    </Section>
  );
}
