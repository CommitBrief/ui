// SPDX-License-Identifier: GPL-3.0-or-later
import { EmptyState, PageHeader } from "../../components";
import { useI18n } from "../../i18n";

// Empty screen: the inner design arrives from Claude Design and its own phase
// wires the data. Only the frame (header + empty state) lives here for now.
export function ReposScreen() {
  const { t } = useI18n();
  return (
    <>
      <PageHeader title={t("screen.repos.title")} description={t("screen.repos.description")} />
      <EmptyState title={t("screen.repos.empty.title")} description={t("screen.repos.empty.description")} />
    </>
  );
}
