// SPDX-License-Identifier: AGPL-3.0-or-later

// What identifies an agent's OAuth client in the UI. Only the host of a Client ID Metadata
// Document URL is checked by the server; it is shown in ASCII (punycode), so look-alike
// letters cannot pass as another domain. A paired agent's own identifier is a claim and is
// shown as such, never styled like a checked domain.

export interface ClientIdentity {
  text: string;
  verified: boolean;
}

/** The pattern the server enforces for free identifiers (decision W7). */
const FREE_IDENTIFIER = /^[a-z0-9][a-z0-9._-]{0,63}$/;

export function clientIdentity(client: string): ClientIdentity | null {
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(client)) {
    try {
      const url = new URL(client);
      return url.protocol === 'https:' && url.hostname !== '' ? { text: url.hostname, verified: true } : null;
    } catch {
      return null;
    }
  }
  return FREE_IDENTIFIER.test(client) ? { text: client, verified: false } : null;
}
