// SPDX-License-Identifier: AGPL-3.0-or-later

// The example command for Claude Code on the browser sign-in page. It is meant to be
// pasted into a shell, so the URL goes in only when it has the plain form of Home-Mandate's
// own MCP address, and always in single quotes: nothing in it can run as a command.

/** https, host of letters, digits, dots and dashes, an optional port and a simple path. */
const SAFE_URL = /^https:\/\/[A-Za-z0-9.-]+(:\d{1,5})?(\/[A-Za-z0-9._~/-]*)?$/;

/** claudeCommand is the "claude mcp add" line for url, or null when url is not of the safe form. */
export function claudeCommand(url: string): string | null {
  return SAFE_URL.test(url) ? `claude mcp add --transport http home-mandate '${url}'` : null;
}
