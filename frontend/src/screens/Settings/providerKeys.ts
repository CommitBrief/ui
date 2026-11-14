// SPDX-License-Identifier: GPL-3.0-or-later

/** The config key of a per-provider field, for example `providers.openai.model`. */
export function providerKey(provider: string, field: "model" | "base_url" | "api_key"): string {
  return `providers.${provider}.${field}`;
}
