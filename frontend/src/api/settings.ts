// SPDX-License-Identifier: GPL-3.0-or-later
import { SettingsService } from "../../bindings/github.com/CommitBrief/ui/internal/adapter/desktop";
import type {
  CLIStatus,
  CLIUpgrade,
  ConfigEntry,
  ConfigFiles,
  ProviderConfig,
  ProviderView,
} from "../../bindings/github.com/CommitBrief/ui/internal/app/models.js";
import type { ConfigField, UI } from "../../bindings/github.com/CommitBrief/ui/internal/domain/settings/models.js";
import type { AuthStatus } from "../../bindings/github.com/CommitBrief/ui/internal/port/models.js";
import { toSettingsError } from "./settingsErrors";

// The Settings screen's only door to the backend. A failed call is rethrown as
// a SettingsError ({code, field}); the backend never sends text.
//
// Secrets: an API key goes out through setConfig and nothing ever comes back.
// There is no function here that returns one, because the service has none.

export { SettingsError, describeSettingsError, settingsErrorKey, toSettingsError } from "./settingsErrors";
export type { AuthStatus, CLIStatus, CLIUpgrade, ConfigEntry, ConfigField, ConfigFiles, ProviderConfig, ProviderView, UI };

/** `repoId` 0 is the user-level config file; any other id is a registered repository's own file. */
export const USER_SCOPE = 0;

async function call<T>(promise: Promise<T>): Promise<T> {
  try {
    return await promise;
  } catch (error) {
    throw toSettingsError(error);
  }
}

// ---- CLI configuration ----

export async function listConfig(): Promise<ConfigEntry[]> {
  return (await call(SettingsService.ListConfig())) ?? [];
}

export function setConfig(key: string, value: string, repoId: number): Promise<void> {
  return call(SettingsService.SetConfig(key, value, { repoId }));
}

export function configFiles(repoId: number): Promise<ConfigFiles> {
  return call(SettingsService.ConfigFiles({ repoId }));
}

/** Opens the config file of a scope in the system's default application. The backend derives the path; only the scope is sent. */
export function openConfigFile(repoId: number): Promise<void> {
  return call(SettingsService.OpenConfigFile({ repoId }));
}

// ---- providers ----

export async function listProviders(): Promise<ProviderView[]> {
  return (await call(SettingsService.Providers())) ?? [];
}

export function providerConfig(name: string): Promise<ProviderConfig> {
  return call(SettingsService.ProviderConfig(name));
}

export function activateProvider(name: string): Promise<void> {
  return call(SettingsService.UseProvider(name, { repoId: USER_SCOPE }));
}

export function testProvider(name: string): Promise<void> {
  return call(SettingsService.TestProvider(name));
}

// ---- CLI version ----

export function cliStatus(): Promise<CLIStatus> {
  return call(SettingsService.CLIStatus());
}

/** Contacts GitHub through the CLI (its one network call); installs nothing. */
export function checkCliUpgrade(): Promise<CLIUpgrade> {
  return call(SettingsService.CheckCLIUpgrade());
}

export function upgradeCli(): Promise<CLIStatus> {
  return call(SettingsService.UpgradeCLI());
}

// ---- GitHub CLI ----

export function githubStatus(): Promise<AuthStatus> {
  return call(SettingsService.GitHubStatus());
}

// ---- the app's own settings ----

export function getUi(): Promise<UI> {
  return call(SettingsService.GetUI());
}

export function updateUi(next: UI): Promise<UI> {
  return call(SettingsService.UpdateUI(next));
}
