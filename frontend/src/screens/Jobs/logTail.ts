// SPDX-License-Identifier: GPL-3.0-or-later

// The live log of a task. The backend masks secrets before a line leaves it;
// this file only makes the text safe and tidy to show, and keeps the buffer
// bounded. The log is plain text: it is rendered as a text node, never as HTML.

/** Lines kept in the panel; older ones fall off the front. */
export const MAX_LOG_LINES = 5000;

// CSI and OSC escape sequences (colors, cursor moves, window titles).
// eslint-disable-next-line no-control-regex
const escapeSequences = /\u001b(?:\[[0-?]*[ -/]*[@-~]|\][^\u0007\u001b]*(?:\u0007|\u001b\\)|[@-Z\\-_])/g;
// Every control character except tab (newlines are split before this runs).
// eslint-disable-next-line no-control-regex
const controlChars = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g;

/**
 * One log line made safe to show: terminal escape sequences and control
 * characters are dropped, and a progress line that rewrites itself with a
 * carriage return keeps only its last state.
 */
export function cleanLogLine(line: string): string {
  const lastReturn = line.lastIndexOf("\r");
  const current = lastReturn >= 0 && lastReturn < line.length - 1 ? line.slice(lastReturn + 1) : line.replace(/\r+$/, "");
  return current.replace(escapeSequences, "").replace(controlChars, "");
}

/** Splits a block of log text into clean lines. A trailing newline does not make an empty last line. */
export function splitLog(text: string): string[] {
  if (text === "") return [];
  const lines = text.split("\n").map(cleanLogLine);
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

/** Appends lines and keeps the newest MAX_LOG_LINES. `dropped` is true when something fell off. */
export function appendCapped(current: readonly string[], added: readonly string[], max = MAX_LOG_LINES): { lines: string[]; dropped: boolean } {
  const all = current.concat(added);
  if (all.length <= max) return { lines: all, dropped: false };
  return { lines: all.slice(all.length - max), dropped: true };
}

/**
 * Joins the tail read from the file with the lines that arrived as events while
 * the read was in flight. An event line can already be inside the tail (the
 * line was written before the file was read), so the longest run where the end
 * of the tail equals the start of the live lines is counted once.
 *
 * Known limit: genuinely repeated lines (say two identical `retrying...` lines
 * written back to back) can be counted once while the read is in flight. It is
 * not lasting: when the task ends the panel reads the file again and replaces
 * everything with the exact record.
 */
export function mergeLogTail(tail: readonly string[], live: readonly string[]): string[] {
  const limit = Math.min(tail.length, live.length);
  for (let overlap = limit; overlap > 0; overlap--) {
    let same = true;
    for (let i = 0; i < overlap; i++) {
      if (tail[tail.length - overlap + i] !== live[i]) {
        same = false;
        break;
      }
    }
    if (same) return tail.concat(live.slice(overlap));
  }
  return tail.concat(live);
}
