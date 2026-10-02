// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { clientIdentity } from './identity.ts';

describe('clientIdentity', () => {
  it('shows the host of a client metadata URL as checked, in its ASCII form', () => {
    expect(clientIdentity('https://claude.ai/oauth/claude-code-client-metadata')).toEqual({ text: 'claude.ai', verified: true });
    expect(clientIdentity('https://Agent.Example:8443/x')).toEqual({ text: 'agent.example', verified: true });
    // A look-alike host shows up as punycode, not as the look-alike letters.
    expect(clientIdentity('https://аpple.com/client')?.text).toBe('xn--pple-43d.com');
  });

  it('shows the identifier of a paired agent as unverified', () => {
    expect(clientIdentity('voice-assistant')).toEqual({ text: 'voice-assistant', verified: false });
    // Looks like a domain, but nobody checked it.
    expect(clientIdentity('accounts.google.com')).toEqual({ text: 'accounts.google.com', verified: false });
  });

  it('shows nothing for http, broken URLs or identifiers the server would not accept', () => {
    expect(clientIdentity('http://insecure.example/x')).toBeNull();
    expect(clientIdentity('https://')).toBeNull();
    expect(clientIdentity('')).toBeNull();
    expect(clientIdentity('аpple')).toBeNull();
    expect(clientIdentity('pair‮evil')).toBeNull();
    expect(clientIdentity('UPPER')).toBeNull();
  });
});
