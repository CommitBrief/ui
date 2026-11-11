// SPDX-License-Identifier: GPL-3.0-or-later
import { useState, type ReactNode } from "react";
import {
  Button,
  Dialog,
  EmptyState,
  Input,
  ModeBadge,
  PageHeader,
  Select,
  SeverityBadge,
  StatusBadge,
  Switch,
  TBody,
  THead,
  Table,
  Td,
  Th,
  Tr,
  runStatuses,
  severities,
  useToast,
} from "../components";
import styles from "./Gallery.module.css";

// Development-only component gallery, served at #/_gallery by `npm run dev` /
// `make dev` and absent from production builds (routes.tsx). Its copy is plain
// development text, deliberately kept out of the i18n catalogs.
const copy = {
  title: "Component gallery",
  description: "Every base component in its states. Development only.",
  buttons: "Buttons",
  fields: "Fields",
  badges: "Badges",
  table: "Table",
  overlays: "Dialog and toast",
  empty: "Empty state",
  inputLabel: "Repository",
  inputHint: "owner/repo, for example CommitBrief/ui",
  inputErrorLabel: "Local path",
  inputError: "This folder does not exist.",
  selectLabel: "Provider",
  switchLabel: "Post reviews to GitHub",
  switchDescription: "Posts under your GitHub identity. Off keeps every job in preview.",
  open: "Open dialog",
  dialogTitle: "Remove repository?",
  dialogDescription: "The repository and its job history are removed from this app. Nothing changes on GitHub.",
  cancel: "Cancel",
  confirm: "Remove",
  removed: "Repository removed",
  toastSuccess: "Success toast",
  toastError: "Error toast",
  toastInfo: "Info toast",
  emptyTitle: "Nothing here",
  emptyDescription: "An empty state explains what will appear and what to do next.",
};

const providers = [
  { value: "anthropic", label: "anthropic" },
  { value: "openai", label: "openai" },
  { value: "ollama", label: "ollama" },
];

const tableHead = ["Repository", "Local path", "Mode", "Last run", "Findings", "Last head", "Notes"];
const tableRows = [
  ["CommitBrief/commitbrief", "/Users/dev/Projects/commitbrief", "preview", "succeeded", "12", "9f2c41a0d3", "Default branch main"],
  ["CommitBrief/ui", "-", "post", "attention", "3", "b4dc02c71e", "No local path: COMMITBRIEF.md is not applied"],
];

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={styles.section}>
      <h2 className={styles.heading}>{title}</h2>
      {children}
    </section>
  );
}

export default function Gallery() {
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [provider, setProvider] = useState("anthropic");

  return (
    <>
      <PageHeader title={copy.title} description={copy.description} />

      <Section title={copy.buttons}>
        <div className={styles.row}>
          <Button variant="primary" icon="send">
            primary
          </Button>
          <Button>secondary</Button>
          <Button variant="ghost">ghost</Button>
          <Button variant="danger">danger</Button>
          <Button variant="primary" size="sm">
            small
          </Button>
          <Button variant="primary" loading>
            loading
          </Button>
          <Button disabled>disabled</Button>
        </div>
      </Section>

      <Section title={copy.fields}>
        <div className={styles.fields}>
          <Input label={copy.inputLabel} hint={copy.inputHint} placeholder="owner/repo" />
          <Input label={copy.inputErrorLabel} error={copy.inputError} defaultValue="/Users/dev/missing" />
          <Select label={copy.selectLabel} options={providers} value={provider} onChange={(e) => setProvider(e.target.value)} />
          <Switch label={copy.switchLabel} description={copy.switchDescription} />
          <Switch label={copy.switchLabel} defaultChecked />
          <Switch label={copy.switchLabel} disabled />
        </div>
      </Section>

      <Section title={copy.badges}>
        <div className={styles.row}>
          {severities.map((severity) => (
            <SeverityBadge key={severity} severity={severity} />
          ))}
        </div>
        <div className={styles.row}>
          {runStatuses.map((status) => (
            <StatusBadge key={status} status={status} />
          ))}
        </div>
        <div className={styles.row}>
          <ModeBadge mode="preview" />
          <ModeBadge mode="post" />
        </div>
      </Section>

      <Section title={copy.table}>
        <Table label={copy.table}>
          <THead>
            <Tr>
              {tableHead.map((name) => (
                <Th key={name}>{name}</Th>
              ))}
            </Tr>
          </THead>
          <TBody>
            {tableRows.map((row) => (
              <Tr key={row[0]}>
                {row.map((cell, index) => (
                  <Td key={tableHead[index]}>
                    {index === 2 ? (
                      <ModeBadge mode={cell === "post" ? "post" : "preview"} />
                    ) : index === 3 ? (
                      <StatusBadge status={cell === "attention" ? "attention" : "succeeded"} />
                    ) : (
                      cell
                    )}
                  </Td>
                ))}
              </Tr>
            ))}
          </TBody>
        </Table>
      </Section>

      <Section title={copy.overlays}>
        <div className={styles.row}>
          <Button onClick={() => setOpen(true)}>{copy.open}</Button>
          <Button onClick={() => toast({ kind: "success", title: copy.toastSuccess, description: copy.inputHint })}>
            {copy.toastSuccess}
          </Button>
          <Button onClick={() => toast({ kind: "error", title: copy.toastError, description: copy.inputError })}>
            {copy.toastError}
          </Button>
          <Button onClick={() => toast({ title: copy.toastInfo })}>{copy.toastInfo}</Button>
        </div>
        <Dialog
          open={open}
          onClose={() => setOpen(false)}
          title={copy.dialogTitle}
          description={copy.dialogDescription}
          footer={
            <>
              <Button onClick={() => setOpen(false)}>{copy.cancel}</Button>
              <Button
                variant="danger"
                onClick={() => {
                  // Close and announce in one handler: the common "confirm, then toast" flow.
                  setOpen(false);
                  toast({ kind: "success", title: copy.removed });
                }}
              >
                {copy.confirm}
              </Button>
            </>
          }
        />
      </Section>

      <Section title={copy.empty}>
        <EmptyState title={copy.emptyTitle} description={copy.emptyDescription} />
      </Section>
    </>
  );
}
