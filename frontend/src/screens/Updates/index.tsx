// SPDX-License-Identifier: GPL-3.0-or-later
import { EmptyState, PageHeader } from "../../components";
import { useI18n } from "../../i18n";

// Empty screen: the inner design arrives from Claude Design and its own phase
// wires the data. Only the frame (header + empty state) lives here for now.
export function UpdatesScreen() {
  const { t } = useI18n();
  return (
    <>
      <PageHeader title={t("screen.updates.title")} description={t("screen.updates.description")} />
      <EmptyState title={t("screen.updates.empty.title")} description={t("screen.updates.empty.description")} />
    </>
  );
}
