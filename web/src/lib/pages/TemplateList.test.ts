// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { setLocale } from '../paraglide/runtime.js';
import TemplateList from './TemplateList.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => cleanup());

async function start(prepare?: (api: MockClient) => Promise<void>, api = createMockClient()) {
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  await prepare?.(api);
  render(TemplateList, { app });
  return { api, app };
}

const section = (name: string) => screen.findByRole('region', { name });
const titles = (region: HTMLElement) => within(region).getAllByRole('heading', { level: 3 }).map((h) => h.textContent);

describe('TemplateList', () => {
  it('lists base templates, hidden ones marked, and the household’s own, each leading to the editor', async () => {
    await start((api) => api.setTemplateHidden('hm-light-climate', true));
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Templates');
    const builtin = await section('Built-in templates');
    expect(titles(builtin)).toEqual(['Read only', 'Light and climate', 'Voice assistant (cautious)']);
    const cards = within(builtin).getAllByRole('article');
    expect(within(cards[1] as HTMLElement).getByText('Hidden')).toBeTruthy();
    expect(within(cards[0] as HTMLElement).queryByText('Hidden')).toBeNull();
    expect(within(cards[2] as HTMLElement).getByText('Lock: unlock, open')).toBeTruthy();
    expect(within(builtin).getByRole('link', { name: 'Read only' }).getAttribute('href')).toBe('#/templates/hm-read-only');
    const own = await section('Your templates');
    expect(titles(own)).toEqual(['empty', 'read-only', 'voice-assistant']);
    expect(within(own).getAllByText(/created on October 1, 2026 by .Markus./)).toHaveLength(3);
    expect(screen.getByRole('link', { name: 'New template' }).getAttribute('href')).toBe('#/templates/_new');
    expect(screen.getByRole('link', { name: 'Back: All mandates' }).getAttribute('href')).toBe('#/mandates');
  });

  it('follows changes of the templates live', async () => {
    const { api } = await start();
    await section('Your templates');
    await api.putTemplate('guest', { draft: (await api.template('empty')).draft, base_digest: null });
    await screen.findByRole('link', { name: 'guest' });
  });

  it('says when there are no own templates yet', async () => {
    await start(async (api) => {
      for (const name of ['empty', 'read-only', 'voice-assistant']) await api.deleteTemplate(name);
    });
    const own = await section('Your templates');
    expect(within(own).getByText(/None yet/)).toBeTruthy();
    expect(within(own).queryByRole('article')).toBeNull();
  });

  it('says that mandates keep applying when loading fails, and recovers on retry', async () => {
    const api = createMockClient();
    let fail = true;
    const templates = api.templates;
    api.templates = async () => (fail ? Promise.reject(new Error('down')) : templates());
    await start(undefined, api);
    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText('Existing mandates keep applying. Please try again.')).toBeTruthy();
    fail = false;
    await fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
    expect(await section('Built-in templates')).toBeTruthy();
  });
});
