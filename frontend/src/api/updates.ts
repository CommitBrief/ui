// SPDX-License-Identifier: GPL-3.0-or-later
import { Events } from "@wailsio/runtime";
import { UpdatesService } from "../../bindings/github.com/CommitBrief/ui/internal/adapter/desktop";
import type { UpdatesView } from "../../bindings/github.com/CommitBrief/ui/internal/app/models.js";
import {
  Code,
  Delegation,
  State,
  type Release,
  type Status,
} from "../../bindings/github.com/CommitBrief/ui/internal/domain/update/models.js";

// The Updates screen's only door to the backend. Errors from the Go side arrive
// as a rejected call whose `cause` is the classified error {code, field}; the
// backend never sends text, the screen words every code itself.

export { Code, Delegation, State };
export type { Release, Status, UpdatesView };

export const updateChannels = ["stable", "prerelease"] as const;
export type UpdateChannel = (typeof updateChannels)[number];

export function isUpdateChannel(value: string): value is UpdateChannel {
  return (updateChannels as readonly string[]).includes(value);
}

/** A classified update failure; `code` is a member of the backend's closed set. */
export class UpdateError extends Error {
  readonly code: string;
  readonly field: string;

  constructor(code: string, field = "") {
    super(code);
    this.name = "UpdateError";
    this.code = code;
    this.field = field;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

/** Reads {code, field} from a rejected binding call; anything unrecognised is "internal". */
export function toUpdateError(error: unknown): UpdateError {
  if (error instanceof UpdateError) return error;
  const cause = isRecord(error) ? error.cause : undefined;
  if (isRecord(cause) && typeof cause.code === "string") {
    return new UpdateError(cause.code, typeof cause.field === "string" ? cause.field : "");
  }
  return new UpdateError(Code.CodeInternal);
}

async function call<T>(promise: Promise<T>): Promise<T> {
  try {
    return await promise;
  } catch (error) {
    throw toUpdateError(error);
  }
}

export function loadUpdates(): Promise<UpdatesView> {
  return call(UpdatesService.View());
}

export function checkForUpdates(): Promise<UpdatesView> {
  return call(UpdatesService.Check());
}

export function setUpdatePreferences(channel: UpdateChannel, autoCheck: boolean): Promise<UpdatesView> {
  return call(UpdatesService.SetPreferences(channel, autoCheck));
}

/** Resolves once the restart has been handed over; the app then quits and comes back as the new version. */
export function installUpdate(): Promise<void> {
  return call(UpdatesService.Install());
}

/** Subscribes to the updater's status changes (download progress included). Returns the unsubscribe function. */
export function onUpdateStatus(callback: (status: Status) => void): () => void {
  return Events.On("updates:status", (event) => callback(event.data));
}
