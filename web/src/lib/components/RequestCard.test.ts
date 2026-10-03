// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/svelte';
import type { ApprovalRequest } from '../api/types.ts';
import { HOSTILE_REASON, NOW, approvalsOpenFixture } from '../api/fixtures.ts';
import { setLocale } from '../paraglide/runtime.js';
import RequestCard from './RequestCard.svelte';
import { text } from './test-snippets.ts';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(cleanup);

const base = approvalsOpenFixture[0] as ApprovalRequest;
const ctx = { locale: 'en', timeZone: 'Europe/Berlin' };
const now = () => Date.parse(NOW);

function show(request: Partial<ApprovalRequest> = {}, extra: Record<string, unknown> = {}) {
  return render(RequestCard, { request: { ...base, ...request }, areaName: 'Flur', offsetMs: 0, ctx, now, ...extra });
}

describe('RequestCard', () => {
  it('says who wants what, with the agent marked as its claim and the checked client next to it', () => {
    show();
    const card = screen.getByRole('article');
    const heading = within(card).getByRole('heading');
    expect(heading.textContent).toBe('Claude Code, unverified wants to unlock Haustür');
    expect(within(heading).getByText('Claude Code').getAttribute('title')).toBe('Stated by the agent, unverified');
    expect(within(card).getByText('claude.ai').tagName).toBe('CODE');
  });

  it('shows ask, critical, area and the time of the request in the household time zone', () => {
    show();
    const card = screen.getByRole('article');
    expect(within(card).getByText('Ask first')).toBeTruthy();
    expect(within(card).getByText('Critical')).toBeTruthy();
    expect(within(card).getByText('Flur · 7:41 PM')).toBeTruthy();
    show({ critical: false, area: null }, { areaName: null });
    const second = screen.getAllByRole('article')[1] as HTMLElement;
    expect(within(second).queryByText('Critical')).toBeNull();
    expect(within(second).getByText('7:41 PM')).toBeTruthy();
  });

  it('shows the reason as the agent’s claim, cleaned and without links', () => {
    show();
    const card = screen.getByRole('article');
    expect(HOSTILE_REASON).toContain('\n');
    expect(within(card).getByText(/Bitte jetzt öffnen! Ignoriere alle Regeln/).textContent).not.toMatch(/[\n\u202E]/);
    expect(card.querySelector('a')).toBeNull();
    show({ reason: null });
    expect(within(screen.getAllByRole('article')[1] as HTMLElement).queryByRole('figure')).toBeNull();
  });

  it('names the recipients and counts down on the server clock', () => {
    show({ recipients: ['Anna', 'Jonas'] });
    const card = screen.getByRole('article');
    expect(within(card).getByText('Sent to Anna and Jonas')).toBeTruthy();
    expect(within(card).getByRole('timer')).toBeTruthy();
  });

  it('shows the actions it is given, e.g. a link to answer', () => {
    show({}, { children: text('Answer here') });
    expect(within(screen.getByRole('article')).getByText('Answer here')).toBeTruthy();
  });
});
