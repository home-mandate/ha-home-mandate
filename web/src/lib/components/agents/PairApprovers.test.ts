// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import type { TemplateApprover, TemplateApprovers } from '../../api/types.ts';
import { setLocale } from '../../paraglide/runtime.js';
import PairApprovers from './PairApprovers.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
});

const person = (over: Partial<TemplateApprover>): TemplateApprover => ({
  user_id: 'u-1',
  name: 'Markus',
  normal: 'push',
  critical: 'push',
  self: false,
  service: false,
  ...over,
});

const answer = (over: Partial<TemplateApprovers>): TemplateApprovers => ({
  people: [person({ self: true })],
  normal: 'not_needed',
  critical: 'reachable',
  ...over,
});

const section = () => screen.getByRole('region', { name: 'Who may approve' });
const text = () => section().textContent ?? '';

describe('PairApprovers', () => {
  it('lists who may approve, the human first, without warnings when someone is reachable', async () => {
    render(PairApprovers, { template: 'hm-voice-cautious', load: async () => answer({}) });
    await screen.findByText('Markus');
    expect(text()).toContain('(you)');
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.queryByRole('link', { name: 'Set up approvers' })).toBeNull();
  });

  it('marks people without a channel and warns when nobody gets critical requests, with the way to the settings', async () => {
    const load = async () =>
      answer({
        people: [
          person({ user_id: 'u-me', normal: 'none', critical: 'none', self: true }),
          person({ user_id: 'u-anna', name: 'Anna', normal: 'push', critical: 'none' }),
          person({ user_id: 'u-uwe', name: 'Uwe', normal: 'ui', critical: 'ui' }),
          person({ user_id: 'u-gone', name: null, normal: 'none', critical: 'none' }),
          person({ user_id: 'u-hm', name: 'Home-Mandate', normal: 'none', critical: 'none', service: true }),
        ],
        critical: 'nobody',
      });
    render(PairApprovers, { template: 'hm-voice-cautious', load });
    await screen.findByText('Anna');
    const items = screen.getAllByRole('listitem').map((li) => li.textContent?.replace(/\s+/g, ' ').trim());
    expect(items).toEqual([
      'Markus (you) no channel for approval requests',
      'Anna not for critical actions',
      'Uwe only in Home-Mandate',
      'Unknown person no channel for approval requests',
      'Home-Mandate Home-Mandate’s own user, never asked',
    ]);
    const alerts = screen.getAllByRole('alert').map((a) => a.textContent ?? '');
    expect(alerts).toHaveLength(1);
    expect(alerts[0]).toContain('Nobody can answer approval requests for critical actions');
    expect(screen.getByRole('link', { name: 'Set up approvers' }).getAttribute('href')).toBe('#/settings/approvers');
  });

  it('warns when nobody gets ordinary requests', async () => {
    render(PairApprovers, {
      template: 't',
      load: async () => answer({ people: [], normal: 'nobody', critical: 'not_needed' }),
    });
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('Nobody can answer approval requests of this mandate yet');
    expect(screen.queryByRole('list')).toBeNull();
  });

  it('shows names from Home Assistant as text, never as markup', async () => {
    const evil = '<img src=x onerror="alert(1)">Anna';
    render(PairApprovers, { template: 't', load: async () => answer({ people: [person({ name: evil })] }) });
    await screen.findByText(evil);
    expect(section().querySelector('img')).toBeNull();
  });

  it('says unknown, never reachable, when Home Assistant could not be asked or the request failed', async () => {
    render(PairApprovers, {
      template: 't',
      load: async () => answer({ people: [person({ normal: 'unknown', critical: 'unknown' })], critical: 'unknown' }),
    });
    await screen.findByText('could not be checked');
    expect((await screen.findByRole('alert')).textContent).toContain('Could not check whether anyone can answer');
    cleanup();
    render(PairApprovers, { template: 't', load: () => Promise.reject(new Error('offline')) });
    expect((await screen.findByRole('alert')).textContent).toContain('Could not check whether anyone can answer');
    expect(screen.queryByRole('list')).toBeNull();
  });

  it('loads again for another template and ignores a late answer for the former one', async () => {
    let release: (a: TemplateApprovers) => void = () => {};
    const slow = new Promise<TemplateApprovers>((resolve) => {
      release = resolve;
    });
    const load = (t: string) => (t === 'first' ? slow : Promise.resolve(answer({ people: [person({ name: 'Second' })] })));
    const view = render(PairApprovers, { template: 'first', load });
    expect(text()).toContain('Loading');
    await view.rerender({ template: 'second', load });
    await screen.findByText('Second');
    release(answer({ people: [person({ name: 'First' })] }));
    await waitFor(() => expect(screen.queryByText('First')).toBeNull());
    expect(screen.getByText('Second')).toBeTruthy();
  });
});
