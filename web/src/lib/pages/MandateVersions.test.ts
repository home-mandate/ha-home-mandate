// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { ApiError } from '../api/client.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import type { MandateDraft, MandateUpdate, Rule } from '../api/types.ts';
import { AppState } from '../app/state.svelte.ts';
import { draftOf } from '../mandate/versions.ts';
import { setLocale } from '../paraglide/runtime.js';
import { toasts } from '../ui/toasts.ts';
import MandateVersions from './MandateVersions.svelte';

const ID = 'mandate-voice';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  for (const toast of toasts.list()) toasts.dismiss(toast.id);
  document.body.replaceChildren();
});

async function store(api: MockClient, change: (draft: MandateDraft) => MandateDraft, options: Partial<MandateUpdate> = {}) {
  const { document: doc, summary } = await api.mandate(ID);
  return api.putMandate(ID, { name: summary.name, draft: change(draftOf(doc)), base_digest: summary.digest, ...options });
}

const withoutCameras = (d: MandateDraft): MandateDraft => ({ ...d, rules: d.rules.slice(0, 4) });
const lightsAsk = (d: MandateDraft): MandateDraft => ({ ...d, rules: d.rules.map((r) => (r.id === 'lights' ? { ...r, decision: 'ask' as const } : r)) });

async function start(prepare?: (api: MockClient) => Promise<void>, options: MockOptions = {}, id = ID) {
  const api = createMockClient(options);
  const app = new AppState(api);
  await app.start();
  await prepare?.(api);
  render(MandateVersions, { app, id });
  return { api, app };
}

/** Three versions: v1 the fixture, v2 without the camera rule, v3 with lights on "ask". */
const three = async (api: MockClient) => {
  await store(api, withoutCameras);
  await store(api, lightsAsk);
};

const list = () => screen.findByRole('list', { name: 'Choose a version to compare with the current one' });

describe('MandateVersions', () => {
  it('lists the versions newest first with author, time and short hash', async () => {
    await start(three);
    const items = within(await list()).getAllByRole('listitem');
    expect(items.map((i) => i.querySelector('strong')?.textContent)).toEqual(['v3', 'v2', 'v1']);
    expect(within(items[0] as HTMLElement).getByText('Current')).toBeTruthy();
    expect(items[0]?.textContent).toContain('mock-2');
    // 17:42 UTC is 7:42 PM in the household's zone (Berlin), not the machine's.
    expect(items[0]?.textContent).toMatch(/Markus · Oct 2, 2026, 7:42\sPM/);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Versions');
    expect(screen.getByRole('link', { name: 'Sprachassistent Küche' }).getAttribute('href')).toBe('#/mandates/mandate-voice');
  });

  it('compares the version before the current one at first', async () => {
    await start(three);
    expect(await screen.findByRole('heading', { name: 'Compare version v2 with v3' })).toBeTruthy();
    const picked = within(await list()).getByRole('button', { current: true });
    expect(picked.textContent).toContain('v2');
    expect(within(picked).getByText('Compare')).toBeTruthy();
    const old = within(screen.getByRole('region', { name: /^v2/ }));
    const current = within(screen.getByRole('region', { name: /^v3/ }));
    expect(old.getAllByRole('listitem')).toHaveLength(4);
    expect(old.getByText('Changed').closest('li')?.textContent).toContain('Lights');
    expect(current.getByText('Changed').closest('li')?.textContent).toContain('Ask first');
    const effects = within(screen.getByRole('region', { name: 'Effect on permissions' }));
    expect(effects.getByText(/Newly needs approval · 8/)).toBeTruthy();
  });

  it('compares any earlier version on request, with added and removed rules', async () => {
    await start(three);
    await fireEvent.click(within(await list()).getByRole('button', { name: /v1/ }));
    expect(await screen.findByRole('heading', { name: 'Compare version v1 with v3' })).toBeTruthy();
    const old = within(screen.getByRole('region', { name: /^v1/ }));
    expect(old.getByText('Removed').closest('li')?.textContent).toContain('Camera');
    expect(old.getAllByRole('listitem')).toHaveLength(5);
    const effects = within(screen.getByRole('region', { name: 'Effect on permissions' }));
    // Without the camera rule the camera falls back to the default: still denied, no effect.
    expect(effects.queryByText(/Newly denied/)).toBeNull();
  });

  it('shows changed settings between the versions', async () => {
    await start(async (api) => {
      await store(api, (d) => ({ ...d, limits: { max_actions_per_hour: 10 } }));
    });
    const settings = within(await screen.findByRole('region', { name: 'Changed settings' }));
    expect(settings.getByText('Rate limit').parentElement?.textContent).toMatch(/Rate limit: 60 → 10/);
    expect(within(screen.getByRole('region', { name: 'Effect on permissions' })).getByText('No effect on permissions.')).toBeTruthy();
  });

  it('says so when there is only one version', async () => {
    await start();
    expect(await screen.findByText(/There is only this one version/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Restore as new version' })).toBeNull();
    expect(within(await list()).queryByRole('button')).toBeNull();
  });

  it('restores an earlier version as a new one through the summary', async () => {
    const { api } = await start(three);
    await fireEvent.click(within(await list()).getByRole('button', { name: /v1/ }));
    await screen.findByRole('heading', { name: 'Compare version v1 with v3' });
    await fireEvent.click(screen.getByRole('button', { name: 'Restore as new version' }));
    const dialog = await screen.findByRole('dialog', { name: 'Restore version 1?' });
    expect(dialog.textContent).toContain('This creates version 4 with the rules, approval settings and rate limit of version 1; validity and name stay as they are now.');
    expect(within(dialog).getByText('Added').closest('div')?.textContent).toContain('Camera');
    expect(within(dialog).getByText(/Newly allowed · 8/)).toBeTruthy();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 4' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const detail = await api.mandate(ID);
    expect(detail.versions).toHaveLength(4);
    expect(detail.document.rules.map((r) => r.id)).toEqual(['lights', 'climate', 'door', 'media', 'no-cameras']);
    expect(detail.document.rules[0]?.decision).toBe('allow');
    expect(toasts.list().map((t) => t.text)).toContain('Mandate saved · version 4');
    await waitFor(async () => expect(within(await list()).getAllByRole('listitem')).toHaveLength(4));
    expect(screen.getByRole('heading', { name: 'Compare version v1 with v4' })).toBeTruthy();
  });

  it('warns when the restored version allows critical actions without approval, and confirms it to the server', async () => {
    const critical: Rule = { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['read', 'unlock'], decision: 'allow', allow_critical: true };
    const { api } = await start(async (a) => {
      await store(a, (d) => ({ ...d, rules: d.rules.map((r) => (r.id === 'door' ? critical : r)) }), { confirm_critical: true });
      await store(a, (d) => ({ ...d, rules: d.rules.filter((r) => r.id !== 'door') }));
    });
    const put = vi.spyOn(api, 'putMandate');
    await screen.findByRole('heading', { name: 'Compare version v2 with v3' });
    await fireEvent.click(screen.getByRole('button', { name: 'Restore as new version' }));
    const dialog = await screen.findByRole('alertdialog', { name: 'Restore version 2?' });
    expect(within(dialog).getByText('Contains critical actions without approval')).toBeTruthy();
    expect(dialog.textContent).toContain('Haustür · unlock (Default: denied → Allowed)');
    expect(within(dialog).getAllByText('Critical actions allowed without approval').length).toBeGreaterThan(0);
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Allow without approval · Save as version 4' }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    expect((put.mock.calls[0]?.[1] as MandateUpdate).confirm_critical).toBe(true);
  });

  it('keeps the summary open and says why when restoring fails', async () => {
    const { api } = await start(three);
    await screen.findByRole('heading', { name: 'Compare version v2 with v3' });
    await fireEvent.click(screen.getByRole('button', { name: 'Restore as new version' }));
    const dialog = await screen.findByRole('dialog', { name: 'Restore version 2?' });
    for (const [code, text] of [
      ['conflict', 'This mandate was changed in the meantime'],
      ['invalid_mandate', 'The server rejected the mandate. Please check the entries.'],
      ['unavailable', 'Couldn’t save. Your changes are still here.'],
    ] as const) {
      api.putMandate = async () => {
        throw new ApiError(code, 0);
      };
      await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 4' }));
      expect(await within(dialog).findByText(text)).toBeTruthy();
    }
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('restores the rules but keeps the current name and validity', async () => {
    const { api } = await start(async (a) => {
      await store(a, (d) => ({ ...d, rules: d.rules.slice(0, 2), expires: '2026-11-30T23:00:00Z' }));
      const { document: doc, summary } = await a.mandate(ID);
      await a.putMandate(ID, { name: 'Neuer Name', draft: { ...draftOf(doc), expires: '2026-12-31T23:00:00Z' }, base_digest: summary.digest });
    });
    // Restore v1 (no end date, five rules) onto v3 (two rules, valid until December 31).
    await fireEvent.click(within(await list()).getByRole('button', { name: /v1/ }));
    await screen.findByRole('heading', { name: 'Compare version v1 with v3' });
    await fireEvent.click(screen.getByRole('button', { name: 'Restore as new version' }));
    const dialog = await screen.findByRole('dialog', { name: 'Restore version 1?' });
    expect(dialog.textContent).toContain('validity and name stay as they are now');
    expect(within(dialog).queryByText('Changed settings')).toBeNull();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 4' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const detail = await api.mandate(ID);
    expect(detail.document.rules).toHaveLength(5);
    expect(detail.document.expires).toBe('2026-12-31T23:00:00Z');
    expect(detail.summary.name).toBe('Neuer Name');
  });

  it('does not offer to restore a revoked mandate', async () => {
    await start(async (api) => {
      await three(api);
      await api.revokeMandate(ID);
    });
    await screen.findByRole('heading', { name: 'Compare version v2 with v3' });
    expect(screen.queryByRole('button', { name: 'Restore as new version' })).toBeNull();
  });

  it('follows new versions stored elsewhere', async () => {
    const { api } = await start(three);
    await screen.findByRole('heading', { name: 'Compare version v2 with v3' });
    await store(api, (d) => ({ ...d, limits: { max_actions_per_hour: 5 } }));
    expect(await screen.findByRole('heading', { name: 'Compare version v2 with v4' })).toBeTruthy();
  });

  it('shows a version without rules as "everything denied"', async () => {
    await start(async (api) => {
      await store(api, (d) => ({ ...d, rules: [] }));
    });
    const current = within(await screen.findByRole('region', { name: /^v2/ }));
    expect(current.getByText('No rules. Everything is denied.')).toBeTruthy();
  });

  it('says that mandates keep applying when loading fails, and that a mandate does not exist', async () => {
    await start(undefined, { failures: { mandate: 'unavailable' } });
    expect(within(await screen.findByRole('alert')).getByText('Existing mandates keep applying unchanged.')).toBeTruthy();
    cleanup();
    await start(undefined, {}, 'mandate-gone');
    expect(await screen.findByRole('heading', { name: 'This mandate doesn’t exist' })).toBeTruthy();
  });

  it('says so when an earlier version cannot be loaded', async () => {
    const { api } = await start(three, { failures: { mandateVersion: 'unavailable' } });
    const alert = (await screen.findByText('Couldn’t load this version.')).closest('[role="alert"]') as HTMLElement;
    // Trying again works, with the button and by picking the same version once more.
    api.mandateVersion = createMockClient().mandateVersion;
    await fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Couldn’t load this version.')).toBeTruthy();
    const fresh = await api.mandate(ID);
    api.mandateVersion = async () => fresh.document;
    await fireEvent.click(within(await list()).getByRole('button', { current: true }));
    expect(await screen.findByRole('region', { name: /^v2/ })).toBeTruthy();
  });

  it('tells versions with the same digest apart: a restored version repeats an earlier one', async () => {
    const { api } = await start(async (a) => {
      await three(a);
      // The server's digest is a hash of the content; after "restore v1" the list is A, C, B, A.
      const real = a.mandate.bind(a);
      a.mandate = async (id) => {
        const detail = await real(id);
        const first = detail.versions.at(-1);
        return first ? { ...detail, versions: [{ ...first, number: 4, created_at: '2026-10-02T18:00:00Z' }, ...detail.versions] } : detail;
      };
    });
    void api;
    const items = within(await list()).getAllByRole('listitem');
    expect(items.map((i) => i.querySelector('strong')?.textContent)).toEqual(['v4', 'v3', 'v2', 'v1']);
    expect(within(await list()).getAllByRole('button', { current: true })).toHaveLength(1);
    await fireEvent.click(within(await list()).getByRole('button', { name: /v1/ }));
    expect(await screen.findByRole('heading', { name: 'Compare version v1 with v4' })).toBeTruthy();
    expect(within(await list()).getByRole('button', { current: true }).textContent).toContain('v1');
    // v1 has the digest of the current version: restoring it would store nothing.
    expect(screen.queryByRole('button', { name: 'Restore as new version' })).toBeNull();
    await fireEvent.click(within(await list()).getByRole('button', { name: /v2/ }));
    expect(await screen.findByRole('button', { name: 'Restore as new version' })).toBeTruthy();
  });
});
