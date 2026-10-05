// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { ApiError } from '../api/client.ts';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient } from '../api/mock.ts';
import { AppState } from '../app/state.svelte.ts';
import { APPROVERS_PLACEHOLDER } from '../mandate/placeholder.ts';
import { setLocale } from '../paraglide/runtime.js';
import { addCriticalTemplate, DOORS } from '../test/critical.ts';
import { toasts } from '../ui/toasts.ts';
import TemplateEditor from './TemplateEditor.svelte';

const PLACEHOLDER_TEXT = 'The household’s approvers and whoever admits the agent';

beforeEach(() => {
  setLocale('en', { reload: false });
  window.location.hash = '';
});
afterEach(() => {
  cleanup();
  for (const toast of toasts.list()) toasts.dismiss(toast.id);
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});

async function start(template: string | null, prepare?: (api: MockClient) => Promise<void>) {
  const api = createMockClient();
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  await prepare?.(api);
  const view = render(TemplateEditor, { app, template, now: Date.parse(NOW) });
  await screen.findByRole('heading', { level: 1 });
  return { api, app, view };
}

const button = (name: string | RegExp) => screen.getByRole('button', { name });
const status = () => document.querySelector('.state')?.textContent?.trim();
const toastTexts = () => toasts.list().map((t) => t.text);
const rate = () => screen.getByLabelText('Rate limit') as HTMLInputElement;

async function changeRate(value: string) {
  await fireEvent.input(rate(), { target: { value } });
}

async function saveAs(name: string, role: 'dialog' | 'alertdialog' = 'dialog') {
  await fireEvent.click(button('Save as new template …'));
  const dialog = await screen.findByRole(role, { name: 'Save as new template' });
  const field = within(dialog).getByLabelText('Template name');
  await fireEvent.input(field, { target: { value: name } });
  await fireEvent.click(within(dialog).getByRole('button', { name: /Create template$/ }));
  return { dialog, field };
}

describe('TemplateEditor: base templates', () => {
  it('shows title, description and mark in the UI language, and only the settings a template has', async () => {
    await start('hm-voice-cautious');
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Voice assistant (cautious)');
    expect(screen.getByText('Built in')).toBeTruthy();
    expect(screen.getByText(/Opens locks only after you confirm it on your phone/)).toBeTruthy();
    expect(screen.getByText('Built-in templates can’t be changed. Save your changes as a new template.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Back: Templates' }).getAttribute('href')).toBe('#/templates');
    expect(rate().value).toBe('60');
    expect(screen.queryByLabelText('Display name')).toBeNull();
    expect(screen.queryByLabelText('Valid from')).toBeNull();
    // No saving in place and no deleting; hiding and saving as new.
    expect(screen.queryByRole('button', { name: 'Save …' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    expect(button('Hide')).toBeTruthy();
    expect(button('Save as new template …')).toBeTruthy();
    expect(status()).toBe('All saved');
  });

  it('shows the approvers placeholder as a sentence, chosen, never as its value', async () => {
    await start('hm-read-only');
    const chip = button(new RegExp(PLACEHOLDER_TEXT));
    expect(chip.getAttribute('aria-pressed')).toBe('true');
    expect(document.body.textContent).not.toContain(APPROVERS_PLACEHOLDER);
    // Markus is set up as an approver and can be added.
    expect(button(/Markus/).getAttribute('aria-pressed')).toBe('false');
  });

  it('saves an edit as a new template with the placeholder kept, and opens it', async () => {
    const { api } = await start('hm-read-only');
    await changeRate('30');
    expect(status()).toBe('1 unsaved change');
    const put = vi.spyOn(api, 'putTemplate');
    await saveAs('my-read-only');
    await waitFor(() => expect(window.location.hash).toBe('#/templates/my-read-only'));
    expect(put).toHaveBeenCalledWith('my-read-only', expect.objectContaining({ base_digest: null }));
    const saved = await api.template('my-read-only');
    expect(saved.draft.limits.max_actions_per_hour).toBe(30);
    expect(saved.draft.approval.approvers).toEqual([APPROVERS_PLACEHOLDER]);
    expect(toastTexts()).toContain('Template ⁨my-read-only⁩ created');
  });

  it('checks the new name as the server does, and says what the server refused', async () => {
    const { api } = await start('hm-read-only');
    const put = vi.spyOn(api, 'putTemplate');
    const { dialog, field } = await saveAs('hm-mine');
    expect(within(dialog).getByText('Names starting with “hm-” belong to the built-in templates.')).toBeTruthy();
    expect(field.getAttribute('aria-invalid')).toBe('true');
    await fireEvent.input(field, { target: { value: 'Guest Room' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create template' }));
    expect(within(dialog).getByText(/Only lowercase letters a–z, digits and hyphens/)).toBeTruthy();
    await fireEvent.input(field, { target: { value: 'read-only' } });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create template' }));
    expect(within(dialog).getByText('A template with this name already exists.')).toBeTruthy();
    expect(put).not.toHaveBeenCalled();
    // Taken meanwhile: the server's conflict is shown at the name.
    await fireEvent.input(field, { target: { value: 'guest' } });
    await api.putTemplate('guest', { draft: (await api.template('empty')).draft, base_digest: null });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Create template' }));
    await waitFor(() => expect(within(dialog).getByText('A template with this name already exists.')).toBeTruthy());
  });

  it('hides a base template and shows it again', async () => {
    const { api } = await start('hm-light-climate');
    await fireEvent.click(button('Hide'));
    await waitFor(() => expect(screen.getByText('Hidden')).toBeTruthy());
    expect((await api.template('hm-light-climate')).hidden).toBe(true);
    expect(toastTexts()).toContain('Template hidden. It is no longer offered when admitting agents.');
    await fireEvent.click(button('Show'));
    await waitFor(() => expect(screen.queryByText('Hidden')).toBeNull());
    expect((await api.template('hm-light-climate')).hidden).toBe(false);
  });
});

describe('TemplateEditor: own templates', () => {
  it('saves in place, naming the version the edit started from', async () => {
    const { api } = await start('voice-assistant');
    const before = await api.template('voice-assistant');
    expect(screen.queryByText('Built in')).toBeNull();
    await changeRate('20');
    const put = vi.spyOn(api, 'putTemplate');
    await fireEvent.click(button('Save …'));
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(within(dialog).getByText('Applies to agents you admit with this template from now on. Existing mandates don’t change.')).toBeTruthy();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save template' }));
    await waitFor(() => expect(status()).toBe('All saved'));
    expect(put).toHaveBeenCalledWith('voice-assistant', expect.objectContaining({ base_digest: before.digest }));
    expect((await api.template('voice-assistant')).draft.limits.max_actions_per_hour).toBe(20);
    expect(toastTexts()).toContain('Template saved');
  });

  it('never overwrites a version saved meanwhile and offers to reload it', async () => {
    const { api } = await start('voice-assistant');
    await changeRate('20');
    // Someone else saves another version.
    const current = await api.template('voice-assistant');
    await api.putTemplate('voice-assistant', { draft: { ...current.draft, rules: [] }, base_digest: current.digest });
    const alert = await screen.findByText('This template was changed in the meantime');
    expect(rate().value).toBe('20');
    expect(button('Save …').getAttribute('aria-disabled')).toBe('true');
    await fireEvent.click(within(alert.closest('.banner') as HTMLElement).getByRole('button', { name: /Load the new state/ }));
    await waitFor(() => expect(rate().value).toBe('60'));
    expect(screen.queryByText('This template was changed in the meantime')).toBeNull();
    expect(status()).toBe('All saved');
  });

  it('shows the conflict when saving runs into a newer version', async () => {
    const { api } = await start('voice-assistant');
    await changeRate('20');
    const put = api.putTemplate;
    api.putTemplate = async (name, update) => {
      const current = await api.template(name);
      await put(name, { draft: { ...current.draft, rules: [] }, base_digest: current.digest });
      return put(name, update);
    };
    await fireEvent.click(button('Save …'));
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save template' }));
    await screen.findByText('This template was changed in the meantime');
    expect(rate().value).toBe('20');
    expect((await api.template('voice-assistant')).draft.rules).toEqual([]);
  });

  it('asks for the separate confirmation (U9) when the template allows critical actions', async () => {
    const { api } = await start(DOORS, addCriticalTemplate);
    const put = vi.spyOn(api, 'putTemplate');
    const { dialog } = await saveAs('doors-copy', 'alertdialog');
    // The dialog flags it and its button is the confirmation.
    expect(dialog.textContent).toContain('Contains critical actions without approval');
    expect(within(dialog).getByRole('button', { name: 'Allow without approval · Create template' })).toBeTruthy();
    await waitFor(() => expect(put).toHaveBeenCalledWith('doors-copy', expect.objectContaining({ confirm_critical: true, base_digest: null })));
  });

  it('keeps an unsaved edit when the page is left and opened again', async () => {
    const { app, view } = await start('voice-assistant');
    await changeRate('20');
    view.unmount();
    render(TemplateEditor, { app, template: 'voice-assistant', now: Date.parse(NOW) });
    await screen.findByRole('heading', { level: 1 });
    expect(rate().value).toBe('20');
    expect(status()).toBe('1 unsaved change');
    // Undone, nothing is kept any more.
    await changeRate('60');
    expect(app.unsavedTemplates.has('voice-assistant')).toBe(false);
  });

  it('keeps the edit of a new template too, and finds a newer version behind a kept edit', async () => {
    const first = await start(null);
    await changeRate('7');
    first.view.unmount();
    render(TemplateEditor, { app: first.app, template: null, now: Date.parse(NOW) });
    await screen.findByRole('heading', { level: 1 });
    expect(rate().value).toBe('7');
    cleanup();

    const { api, app, view } = await start('voice-assistant');
    await changeRate('20');
    view.unmount();
    const current = await api.template('voice-assistant');
    await api.putTemplate('voice-assistant', { draft: { ...current.draft, rules: [] }, base_digest: current.digest });
    render(TemplateEditor, { app, template: 'voice-assistant', now: Date.parse(NOW) });
    await screen.findByText('This template was changed in the meantime');
    expect(rate().value).toBe('20');
  });

  it('shows that the template is gone when saving finds it deleted', async () => {
    const { api } = await start('voice-assistant');
    await changeRate('20');
    const remove = api.deleteTemplate;
    api.putTemplate = async (name) => {
      await remove(name);
      throw new ApiError('not_found', 404);
    };
    await fireEvent.click(button('Save …'));
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save template' }));
    expect(await screen.findByText('This template was deleted in the meantime')).toBeTruthy();
    expect(rate().value).toBe('20');
  });

  it('opens no dialog with Ctrl+S while another dialog is open', async () => {
    await start('voice-assistant');
    await changeRate('20');
    await fireEvent.click(button('Load template …'));
    const dialog = await screen.findByRole('dialog', { name: 'Load template' });
    await fireEvent.keyDown(window, { key: 's', ctrlKey: true });
    expect(screen.queryByRole('dialog', { name: 'Save changes?' })).toBeNull();
    // The open template closes the dialog instead of loading it again.
    await fireEvent.click(within(dialog).getByRole('link', { name: 'voice-assistant' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Load template' })).toBeNull());
    expect(rate().value).toBe('20');
    await fireEvent.keyDown(window, { key: 's', ctrlKey: true });
    expect(await screen.findByRole('dialog', { name: 'Save changes?' })).toBeTruthy();
  });

  it('deletes after a confirmation and goes back to the list', async () => {
    const { api } = await start('voice-assistant');
    await fireEvent.click(button('Delete'));
    const dialog = await screen.findByRole('alertdialog', { name: /Delete template/ });
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Cancel' })));
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(window.location.hash).toBe('#/templates'));
    await expect(api.template('voice-assistant')).rejects.toMatchObject({ code: 'not_found' });
  });

  it('says when the template was deleted meanwhile and keeps the edit', async () => {
    const { api } = await start('voice-assistant');
    await changeRate('20');
    await api.deleteTemplate('voice-assistant');
    expect(await screen.findByText('This template was deleted in the meantime')).toBeTruthy();
    expect(rate().value).toBe('20');
    expect(button('Save …').getAttribute('aria-disabled')).toBe('true');
  });

  it('shows the problems instead of saving an invalid template', async () => {
    const { api } = await start('voice-assistant');
    await changeRate('0');
    const put = vi.spyOn(api, 'putTemplate');
    await fireEvent.click(button('Save …'));
    expect(await screen.findByText(/1 error/)).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(put).not.toHaveBeenCalled();
  });

  it('says so when the server refuses the template', async () => {
    const { api } = await start('voice-assistant');
    await changeRate('20');
    api.putTemplate = async () => Promise.reject(new ApiError('invalid_mandate', 422, '/draft/rules/0'));
    await fireEvent.click(button('Save …'));
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save template' }));
    await waitFor(() => expect(within(dialog).getByRole('alert').textContent).toContain('The server rejected the template'));
  });
});

describe('TemplateEditor: new and loading', () => {
  it('starts a new template empty, asking the placeholder, with the household’s defaults', async () => {
    const { api } = await start(null);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('New template');
    expect(button(new RegExp(PLACEHOLDER_TEXT)).getAttribute('aria-pressed')).toBe('true');
    expect(rate().value).toBe('60');
    expect(screen.queryByRole('button', { name: 'Save …' })).toBeNull();
    const put = vi.spyOn(api, 'putTemplate');
    await saveAs('blank');
    await waitFor(() => expect(window.location.hash).toBe('#/templates/blank'));
    expect(put.mock.calls[0]?.[1].draft).toMatchObject({ rules: [], approval: { timeout: 'PT2M', approvers: [APPROVERS_PLACEHOLDER] } });
  });

  it('loads any template, hidden ones too, and warns about unsaved changes', async () => {
    await start('read-only', async (api) => api.setTemplateHidden('hm-light-climate', true));
    await fireEvent.click(button('Load template …'));
    let dialog = await screen.findByRole('dialog', { name: 'Load template' });
    const links = within(dialog).getAllByRole('link');
    expect(links.map((l) => l.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
      'Read only Built in',
      'Light and climate Built in Hidden',
      'Voice assistant (cautious) Built in',
      'empty',
      'read-only',
      'voice-assistant',
    ]);
    expect(links[1]?.getAttribute('href')).toBe('#/templates/hm-light-climate');
    expect(links[4]?.getAttribute('aria-current')).toBe('page');
    expect(within(dialog).queryByText(/unsaved changes will be lost/)).toBeNull();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await changeRate('5');
    await fireEvent.click(button('Load template …'));
    dialog = await screen.findByRole('dialog', { name: 'Load template' });
    expect(within(dialog).getByText(/unsaved changes will be lost/)).toBeTruthy();
  });

  it('looks like a missing page for an unknown template', async () => {
    const api = createMockClient();
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    render(TemplateEditor, { app, template: 'nope', now: Date.parse(NOW) });
    expect(await screen.findByText('This template doesn’t exist')).toBeTruthy();
  });
});
