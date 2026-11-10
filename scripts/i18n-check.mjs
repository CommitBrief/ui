#!/usr/bin/env node
// i18n-check.mjs - fail when frontend/src/i18n/en.json and tr.json do not
// carry exactly the same set of keys. Nested objects are compared by their
// dotted path. Usage: node scripts/i18n-check.mjs [i18n-dir]

import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dir = process.argv[2] ? resolve(process.argv[2]) : join(root, "frontend", "src", "i18n");
const locales = ["en", "tr"];

function flatten(value, prefix = "") {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return [prefix];
  }
  const keys = Object.keys(value);
  if (keys.length === 0 && prefix === "") {
    return [];
  }
  return keys.flatMap((key) => flatten(value[key], prefix ? `${prefix}.${key}` : key));
}

const catalogs = {};
for (const locale of locales) {
  const file = join(dir, `${locale}.json`);
  try {
    const parsed = JSON.parse(readFileSync(file, "utf8"));
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("top level must be an object");
    }
    catalogs[locale] = new Set(flatten(parsed));
  } catch (err) {
    console.error(`i18n-check: cannot load ${file}: ${err.message}`);
    process.exit(1);
  }
}

let failed = false;
for (const locale of locales) {
  const other = locales.find((l) => l !== locale);
  const missing = [...catalogs[other]].filter((key) => !catalogs[locale].has(key)).sort();
  if (missing.length > 0) {
    failed = true;
    console.error(`i18n-check: ${locale}.json is missing ${missing.length} key(s) present in ${other}.json:`);
    for (const key of missing) console.error(`  ${key}`);
  }
}

if (failed) process.exit(1);
console.log(`i18n-check: ok (${catalogs.en.size} keys, en/tr in parity)`);
