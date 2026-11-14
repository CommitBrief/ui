// SPDX-License-Identifier: GPL-3.0-or-later
import { DialogsService, ReposService } from "../../bindings/github.com/CommitBrief/ui/internal/adapter/desktop";
import type { ImportResult, RemoteSuggestion, RepoInput } from "../../bindings/github.com/CommitBrief/ui/internal/app/models.js";
import type { Repository } from "../../bindings/github.com/CommitBrief/ui/internal/domain/repo/models.js";
import { toRepoError } from "./repoErrors";

// The Repos screen's only door to the backend. A failed call is rethrown as a
// RepoError ({code, field, entry}); the backend never sends text.

export { RepoError, describeRepoError, errorFormField, repoErrorKey, toRepoError } from "./repoErrors";
export type { ImportResult, RemoteSuggestion, RepoInput, Repository };

async function call<T>(promise: Promise<T>): Promise<T> {
  try {
    return await promise;
  } catch (error) {
    throw toRepoError(error);
  }
}

export async function listRepos(): Promise<Repository[]> {
  return (await call(ReposService.List())) ?? [];
}

export function addRepo(input: RepoInput): Promise<Repository> {
  return call(ReposService.Add(input));
}

export function updateRepo(id: number, input: RepoInput): Promise<Repository> {
  return call(ReposService.Update(id, input));
}

export function setRepoEnabled(id: number, enabled: boolean): Promise<Repository> {
  return call(ReposService.SetEnabled(id, enabled));
}

export function deleteRepo(id: number): Promise<void> {
  return call(ReposService.Delete(id));
}

/** Reads owner/name from the origin remote of the checkout at `path`. */
export function suggestFromLocalPath(path: string): Promise<RemoteSuggestion> {
  return call(ReposService.SuggestFromLocalPath(path));
}

export function exportRepos(): Promise<string> {
  return call(ReposService.ExportJSON());
}

export function importRepos(document: string): Promise<ImportResult> {
  return call(ReposService.ImportJSON(document));
}

/**
 * Opens the native folder chooser (an app service method, not a raw file API)
 * and resolves with the chosen folder, or "" when the user canceled.
 */
export function pickFolder(title: string): Promise<string> {
  return call(DialogsService.PickFolder(title));
}

/**
 * Opens the native save dialog and writes the export document where the user
 * chose (the file is written by the backend; the webview supplies no content and
 * no path). Resolves with the file's name, or "" when the user canceled.
 */
export function saveRepoExport(title: string, fileName: string): Promise<string> {
  return call(DialogsService.SaveRepoExport(title, fileName));
}

/** Import files over this size are refused by the backend (app.MaxImportBytes). */
export const MAX_IMPORT_BYTES = 2 * 1024 * 1024;
