// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { ApiError } from '../api/client.ts';
import { NOW } from '../api/fixtures.ts';
import { createMockClient, type MockClient, type MockOptions } from '../api/mock.ts';
import type { MandateDraft, MandateUpdate, Rule } from '../api/types.ts';
import { AppState } from '../app/state.svelte.ts';
import { draftOf } from '../mandate/versions.ts';
import { setLocale } from '../paraglide/runtime.js';
import { toasts } from '../ui/toasts.ts';
import MandateEditor from './MandateEditor.svelte';

const ID = 'mandate-voice';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  for (const toast of toasts.list()) toasts.dismiss(toast.id);
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});

async function start(options: MockOptions = {}, prepare?: (api: MockClient) => Promise<void>, id = ID) {
  const api = createMockClient(options);
  const app = new AppState(api, () => Date.parse(NOW));
  await app.start();
  await prepare?.(api);
  const view = render(MandateEditor, { app, id, now: Date.parse(NOW) });
  return { api, app, view };
}

/** store saves another version behind the editor's back. */
async function store(api: MockClient, change: (draft: MandateDraft) => MandateDraft, name = 'Sprachassistent Küche') {
  const { document: doc, summary } = await api.mandate(ID);
  return api.putMandate(ID, { name, draft: change(draftOf(doc)), base_digest: summary.digest });
}

const rulesRegion = () => screen.findByRole('region', { name: /^Rules/ });
const cards = async () => within(await rulesRegion()).getAllByRole('listitem');
const card = async (n: number) => within((await cards())[n - 1] as HTMLElement);
const status = () => document.querySelector('.state')?.textContent?.trim();
const announcedState = () => screen.getAllByRole('status').find((s) => /saved|unsaved/i.test(s.textContent ?? ''))?.textContent?.trim();
const live = () => document.querySelector('[aria-live="polite"].hm-visually-hidden')?.textContent ?? '';
const saveButton = () => screen.getByRole('button', { name: 'Save …' });
const toastTexts = () => toasts.list().map((t) => t.text);

async function open(n: number) {
  await fireEvent.click((await card(n)).getByRole('button', { name: `Edit: Rule ${n}` }));
  return card(n);
}

describe('MandateEditor', () => {
  it('shows the mandate: name, status, version, settings and its rules as sentences', async () => {
    await start();
    expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe('Sprachassistent Küche');
    expect(screen.getByText('Active')).toBeTruthy();
    expect(screen.getByRole('button', { name: /^Mandate version v1 · fixture-/ })).toBeTruthy();
    expect((screen.getByLabelText('Display name') as HTMLInputElement).value).toBe('Sprachassistent Küche');
    expect((screen.getByLabelText('Valid from') as HTMLInputElement).value).toBe('2026-10-01');
    expect((screen.getByLabelText('Valid until (optional)') as HTMLInputElement).value).toBe('');
    expect((screen.getByLabelText('Rate limit') as HTMLInputElement).value).toBe('60');
    expect(screen.getByRole('link', { name: 'All mandates' }).getAttribute('href')).toBe('#/mandates');
    expect(screen.getByRole('link', { name: 'Versions' }).getAttribute('href')).toBe('#/mandates/mandate-voice/versions');

    const list = await cards();
    expect(list).toHaveLength(6);
    expect(list[0]?.textContent).toContain('Rule 1: Lights: read, turn on, turn off, adjust');
    expect(within(list[0] as HTMLElement).getByText('Allowed')).toBeTruthy();
    expect(within(list[0] as HTMLElement).getByText('Matches 2 devices')).toBeTruthy();
    expect(list[2]?.textContent).toContain('Haustür: read, unlock');
    expect(within(list[3] as HTMLElement).getByText(/07:00\sAM to 10:00\sPM/)).toBeTruthy();
    expect(list[5]?.textContent).toContain('Everything else: denied');
    expect(status()).toBe('All saved');
    expect(saveButton().getAttribute('aria-disabled')).toBe('true');
  });

  it('edits a rule in place: sentence, counter and save button follow', async () => {
    await start();
    const first = await open(1);
    const adjust = first.getByRole('button', { name: 'adjust' });
    expect(adjust.getAttribute('aria-pressed')).toBe('true');
    await fireEvent.click(adjust);
    expect((await card(1)).getByText('read, turn on, turn off')).toBeTruthy();
    expect(status()).toBe('1 unsaved change');
    expect(saveButton().getAttribute('aria-disabled')).toBeNull();

    await fireEvent.click(first.getByRole('radio', { name: 'Deny' }));
    expect((await card(1)).getByText('Denied')).toBeTruthy();
    await fireEvent.click(first.getByRole('button', { name: 'Done' }));
    expect((await card(1)).queryByRole('radiogroup')).toBeNull();
    expect(document.activeElement).toBe((await card(1)).getByRole('button', { name: 'Edit: Rule 1' }));
  });

  it('opens one rule at a time and moves the focus into the form', async () => {
    await start();
    const first = await open(1);
    await tick();
    expect(document.activeElement).toBe(first.getByLabelText('Category'));
    await open(2);
    expect((await card(1)).queryByRole('radiogroup')).toBeNull();
    expect((await card(2)).getByRole('radiogroup')).toBeTruthy();
  });

  it('changes the scope of a rule through category, area and device', async () => {
    await start();
    const first = await open(1);
    await fireEvent.change(first.getByLabelText('Area'), { target: { value: 'kitchen' } });
    expect((await card(1)).getAllByText('Matches 1 device').length).toBeGreaterThan(0);
    expect((await cards())[0]?.textContent).toContain('Lights · Küche:');
    await fireEvent.change(first.getByLabelText('Single device (optional)'), { target: { value: 'light.kitchen' } });
    expect((await cards())[0]?.textContent).toContain('Küchenlicht:');
    await fireEvent.change(first.getByLabelText('Category'), { target: { value: 'all' } });
    expect((await cards())[0]?.textContent).toContain('All devices');
    // The light actions do not exist for "all devices" as a vocabulary, but stay selected.
    expect(first.getByRole('button', { name: 'turn on' }).getAttribute('aria-pressed')).toBe('true');
  });

  it('flags an action that does not fit the category until it is removed', async () => {
    await start();
    const door = await open(3);
    await fireEvent.change(door.getByLabelText('Category'), { target: { value: 'light' } });
    expect((await card(3)).getByText('Action “unlock” doesn’t fit “Lights”.')).toBeTruthy();
    // Announced once, by the summary; the message at the field is plain text.
    expect(screen.getAllByRole('alert').map((a) => a.textContent?.trim())).toEqual(['1 error. Please fix before saving.']);
    await fireEvent.click((await card(3)).getByRole('button', { name: 'unlock' }));
    expect((await card(3)).queryByText('Action “unlock” doesn’t fit “Lights”.')).toBeNull();
    expect(screen.queryByText(/Please fix before saving/)).toBeNull();
  });

  it('says that write actions do not include reading and what "all actions" includes', async () => {
    await start();
    const door = await open(3);
    await fireEvent.click(door.getByRole('button', { name: 'read' }));
    expect(door.getByText('“Read” isn’t included. Write access doesn’t include read access.')).toBeTruthy();
    await fireEvent.click(door.getByRole('button', { name: 'all actions' }));
    expect(door.getByText('“All actions” includes critical actions: unlock, open.')).toBeTruthy();
  });

  it('adds rules as read-only "ask" and opens them', async () => {
    await start();
    await fireEvent.click(await screen.findByRole('button', { name: 'Add rule' }));
    const added = await card(6);
    expect((await cards())[5]?.textContent).toContain('Rule 6: Lights: read');
    expect(added.getByRole('radio', { name: 'Ask' }).getAttribute('aria-checked')).toBe('true');
    expect(added.getByText(/Uses the mandate default \(2 minutes\)/)).toBeTruthy();
    await tick();
    expect(document.activeElement).toBe(added.getByLabelText('Category'));
    expect(screen.getByText('New rules start with “Ask first”.')).toBeTruthy();
    expect(status()).toBe('1 unsaved change');
  });

  it('moves rules with the buttons, announces the new position and keeps the focus', async () => {
    await start();
    const down = (await card(1)).getByRole('button', { name: 'Move rule 1 down' });
    down.focus();
    await fireEvent.click(down);
    expect((await cards())[1]?.textContent).toContain('Lights');
    await tick();
    expect(live()).toBe('Rule 1 is now at position 2 of 5.');
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Move rule 2 down');
    expect(status()).toBe('1 unsaved change');
    // At the ends the buttons stay focusable but do nothing.
    const up = (await card(1)).getByRole('button', { name: 'Move rule 1 up' });
    expect(up.getAttribute('aria-disabled')).toBe('true');
    await fireEvent.click(up);
    await tick();
    expect((await cards())[0]?.textContent).toContain('Climate');
    expect(live()).toBe('Rule 1 is already first.');
    await fireEvent.click((await card(5)).getByRole('button', { name: 'Move rule 5 down' }));
    await tick();
    expect(live()).toBe('Rule 5 is already last.');
    // The counter is visible; screen readers hear the state only when it flips.
    expect(announcedState()).toBe('Unsaved changes');
  });

  it('moves rules by drag and drop', async () => {
    await start();
    const handle = (await cards())[0]?.querySelector('[draggable="true"]') as HTMLElement;
    await fireEvent.dragStart(handle);
    await fireEvent.dragOver((await cards())[2] as HTMLElement);
    await fireEvent.drop((await cards())[2] as HTMLElement);
    await tick();
    expect((await cards())[2]?.textContent).toContain('Lights');
    expect(live()).toBe('Rule 1 is now at position 3 of 5.');
  });

  it('deletes a rule with an undo and moves the focus to the next rule', async () => {
    await start();
    const second = await open(2);
    await fireEvent.click(second.getByRole('button', { name: 'Delete' }));
    expect(await cards()).toHaveLength(5);
    expect((await cards())[1]?.textContent).toContain('Haustür');
    expect(document.activeElement).toBe((await card(2)).getByRole('button', { name: 'Edit: Rule 2' }));
    const [toast] = toasts.list();
    expect(toast).toMatchObject({ kind: 'undo', text: 'Rule 2 deleted.' });
    expect(live()).toBe('Rule 2 deleted.');
    toasts.act(toast?.id ?? 0);
    await tick();
    await tick();
    expect((await cards())[1]?.textContent).toContain('Climate');
    expect(status()).toBe('All saved');
    expect(live()).toBe('Rule 2 restored.');
    expect(document.activeElement).toBe((await card(2)).getByRole('button', { name: 'Edit: Rule 2' }));
  });

  it('keeps an undo button under the list that the keyboard reaches in time, until the next edit', async () => {
    await start();
    const second = await open(2);
    await fireEvent.click(second.getByRole('button', { name: 'Delete' }));
    const undo = screen.getByRole('button', { name: 'Undo deleting rule 2' });
    await fireEvent.click(undo);
    await tick();
    expect(await cards()).toHaveLength(6);
    expect(toasts.list()).toEqual([]);
    expect(screen.queryByRole('button', { name: 'Undo deleting rule 2' })).toBeNull();

    // After another edit the deleted rule would no longer fit where it was: no undo then.
    await fireEvent.click((await open(2)).getByRole('button', { name: 'Delete' }));
    await fireEvent.click((await card(1)).getByRole('button', { name: 'Move rule 1 down' }));
    expect(screen.queryByRole('button', { name: /^Undo deleting/ })).toBeNull();
    const [stale] = toasts.list();
    toasts.act(stale?.id ?? 0);
    await tick();
    expect(await cards()).toHaveLength(5);
  });

  it('does not let an undo reach into a version taken over from the server', async () => {
    const { api } = await start();
    const second = await open(2);
    await fireEvent.click(second.getByRole('button', { name: 'Delete' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Undo deleting rule 2' }));
    await fireEvent.click((await open(2)).getByRole('button', { name: 'Delete' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Undo deleting rule 2' }));
    await waitFor(() => expect(status()).toBe('All saved'));
    await fireEvent.click((await open(2)).getByRole('button', { name: 'Delete' }));
    expect(toasts.list()).toHaveLength(1);
    // Discarding through a conflict replaces the edit: the undo of the old edit goes with it.
    await store(api, (d) => ({ ...d, rules: d.rules.slice(0, 4) }));
    const banner = (await screen.findByText('This mandate was changed in the meantime')).closest('[role="alert"]') as HTMLElement;
    await fireEvent.click(within(banner).getByRole('button', { name: 'Discard my changes' }));
    expect(toasts.list().filter((t) => t.kind === 'undo')).toEqual([]);
    expect(screen.queryByRole('button', { name: /^Undo deleting/ })).toBeNull();
  });

  it('moves the focus to the next rule that is shown when a search hides some', async () => {
    const many = (d: MandateDraft): MandateDraft => ({
      ...d,
      rules: [...d.rules, ...Array.from({ length: 6 }, (_, i): Rule => ({ id: `extra-${i}`, resource: { category: 'switch' }, actions: ['read'], decision: 'deny' }))],
    });
    await start({}, async (api) => void (await store(api, many)));
    await fireEvent.input(await screen.findByLabelText('Search rules'), { target: { value: 'li' } });
    // Shown: lights (1) and climate (2); deleting lights leaves climate as the next one.
    const first = await open(1);
    await fireEvent.click(first.getByRole('button', { name: 'Delete' }));
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Edit: Rule 1');
  });

  it('moves the focus to "Add rule" when the last rule is deleted', async () => {
    await start();
    const last = await open(5);
    await fireEvent.click(last.getByRole('button', { name: 'Delete' }));
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Add rule' }));
  });

  it('sets a time window and weekdays', async () => {
    await start();
    const first = await open(1);
    await fireEvent.click(first.getByLabelText('Time window'));
    expect(first.getByText(/10:00\sPM to 06:00\sAM the next day · Times in household time \(Europe\/Berlin\)/)).toBeTruthy();
    expect((first.getByLabelText('From') as HTMLInputElement).value).toBe('22:00');
    await fireEvent.input(first.getByLabelText('To'), { target: { value: '22:00' } });
    await fireEvent.focusOut(first.getByLabelText('To'));
    expect(first.getByText('Start and end of the time window are the same.')).toBeTruthy();
    await fireEvent.input(first.getByLabelText('To'), { target: { value: '23:30' } });
    expect(first.getByText('Every day')).toBeTruthy();
    await fireEvent.click(within(first.getByRole('group', { name: 'Weekdays' })).getByRole('button', { name: 'Sun' }));
    await fireEvent.click(within(first.getByRole('group', { name: 'Weekdays' })).getByRole('button', { name: 'Sat' }));
    expect((await cards())[0]?.textContent).toMatch(/Mon\s–\sFri · 10:00\sPM to 11:30\sPM/);
  });

  it('gives an "ask" rule its own timeout and approvers, and takes them back', async () => {
    await start();
    const door = await open(3);
    await fireEvent.click(door.getByRole('button', { name: 'Own timeout and approvers' }));
    const own = within(door.getByRole('group', { name: 'Approval timeout' }));
    expect((own.getByRole('spinbutton') as HTMLInputElement).value).toBe('2');
    await fireEvent.input(own.getByRole('spinbutton'), { target: { value: '45' } });
    await fireEvent.change(own.getByRole('combobox'), { target: { value: 's' } });
    expect(status()).toBe('1 unsaved change');
    await fireEvent.click(door.getByRole('button', { name: 'Use the mandate default' }));
    expect(status()).toBe('All saved');
  });
});

describe('critical actions without approval', () => {
  it('needs the separate confirmation; the safe choice has the focus', async () => {
    const { api } = await start();
    const put = vi.spyOn(api, 'putMandate');
    const door = await open(3);
    await fireEvent.click(door.getByRole('radio', { name: 'Allow' }));
    expect((await card(3)).getAllByText(/Becomes an approval request because the action is critical/).length).toBeGreaterThan(0);

    const toggle = door.getByRole('switch', { name: 'Also allow critical actions without approval' });
    await fireEvent.click(toggle);
    // Switching on does not switch it on.
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    const confirm = door.getByRole('group', { name: 'Allow critical actions without approval?' });
    expect(confirm.textContent).toContain('\u2068Sprachassistent\u2069 could then, without confirmation: unlock. Affects 1 device.');
    await tick();
    expect(document.activeElement).toBe(within(confirm).getByRole('button', { name: 'Keep approval' }));

    await fireEvent.click(within(confirm).getByRole('button', { name: 'Keep approval' }));
    expect(toggle.getAttribute('aria-checked')).toBe('false');
    expect(door.queryByRole('group', { name: 'Allow critical actions without approval?' })).toBeNull();

    await fireEvent.click(toggle);
    await fireEvent.click(door.getByRole('button', { name: 'Allow without approval' }));
    expect(toggle.getAttribute('aria-checked')).toBe('true');
    expect((await card(3)).getAllByText('Critical actions allowed without approval').length).toBeGreaterThan(0);

    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('alertdialog', { name: 'Save changes?' });
    const flag = within(dialog).getByText('Contains critical actions without approval').closest('[id]') as HTMLElement;
    expect(dialog.getAttribute('aria-describedby')).toContain(flag.id);
    // The changed rule does not read as a plain "Allowed", and the button says what it confirms.
    expect(within(dialog).getByText('Critical actions allowed without approval')).toBeTruthy();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Allow without approval · Save as version 2' }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    const update = put.mock.calls[0]?.[1] as MandateUpdate;
    expect(update.confirm_critical).toBe(true);
    expect(update.draft.rules[2]).toMatchObject({ decision: 'allow', allow_critical: true });
  });

  it('takes the confirmation back when the rule changes, and says so', async () => {
    await start();
    const door = await open(3);
    await fireEvent.click(door.getByRole('radio', { name: 'Allow' }));
    await fireEvent.click(door.getByRole('switch'));
    await fireEvent.click(door.getByRole('button', { name: 'Allow without approval' }));
    expect(door.getByRole('switch').getAttribute('aria-checked')).toBe('true');
    await fireEvent.click(door.getByRole('button', { name: /^open/ }));
    await tick();
    expect(door.getByRole('switch').getAttribute('aria-checked')).toBe('false');
    expect(live()).toBe('The permission without approval was withdrawn because the rule changed.');
  });

  it('switches off without a confirmation and without that message', async () => {
    await start();
    const door = await open(3);
    await fireEvent.click(door.getByRole('radio', { name: 'Allow' }));
    await fireEvent.click(door.getByRole('switch'));
    await fireEvent.click(door.getByRole('button', { name: 'Allow without approval' }));
    await fireEvent.click(door.getByRole('switch'));
    expect(door.getByRole('switch').getAttribute('aria-checked')).toBe('false');
    expect(live()).toBe('');
  });
});

describe('validation', () => {
  it('shows errors after the field was left, never while typing', async () => {
    await start();
    const group = within(await screen.findByRole('group', { name: 'Approval timeout' }));
    await fireEvent.input(group.getByRole('spinbutton'), { target: { value: '5' } });
    await fireEvent.change(group.getByRole('combobox'), { target: { value: 's' } });
    expect(screen.queryByText('Timeout must be between 10 seconds and 1 hour.')).toBeNull();
    await fireEvent.blur(group.getByRole('spinbutton'));
    expect(screen.getAllByText('Timeout must be between 10 seconds and 1 hour.').length).toBeGreaterThan(0);
    expect(group.getByRole('spinbutton').getAttribute('aria-invalid')).toBe('true');
  });

  it('lists all errors on a save attempt, with links to the fields, and does not open the summary', async () => {
    await start();
    const name = await screen.findByLabelText('Display name');
    await fireEvent.input(name, { target: { value: ' ' } });
    await fireEvent.input(screen.getByLabelText('Rate limit'), { target: { value: '0' } });
    const first = await open(1);
    for (const action of ['read', 'turn on', 'turn off', 'adjust']) await fireEvent.click(first.getByRole('button', { name: action }));
    await fireEvent.click(first.getByRole('button', { name: 'Done' }));

    await fireEvent.click(saveButton());
    expect(screen.queryByRole('dialog')).toBeNull();
    const list = screen.getByText('3 errors. Please fix before saving.').closest('[role="group"]') as HTMLElement;
    expect(document.activeElement).toBe(list);
    const links = within(list).getAllByRole('button').map((b) => b.textContent);
    expect(links).toEqual(['Enter a name of up to 80 characters.', 'The rate limit must be between 1 and 1,000.', 'Rule 1: Choose at least one action.']);
    expect(saveButton().getAttribute('aria-describedby')).toBe(list.id);
    // A collapsed rule keeps its message.
    expect((await card(1)).getByText('Choose at least one action.')).toBeTruthy();

    await fireEvent.click(within(list).getByRole('button', { name: 'Rule 1: Choose at least one action.' }));
    await tick();
    expect((await card(1)).getByRole('radiogroup')).toBeTruthy();
    await fireEvent.click(within(list).getByRole('button', { name: 'Enter a name of up to 80 characters.' }));
    await tick();
    expect(document.activeElement).toBe(name);
  });

  it('checks the validity dates as days in the household time zone', async () => {
    const { api } = await start();
    const put = vi.spyOn(api, 'putMandate');
    const until = await screen.findByLabelText('Valid until (optional)');
    await fireEvent.input(until, { target: { value: '2026-09-01' } });
    await fireEvent.blur(until);
    expect(screen.getAllByText('“Valid until” must not be before “Valid from”.').length).toBeGreaterThan(0);
    await fireEvent.input(until, { target: { value: '2026-12-31' } });
    await fireEvent.input(screen.getByLabelText('Valid from'), { target: { value: '2026-11-01' } });
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(within(dialog).getByText('Changed settings')).toBeTruthy();
    expect(dialog.textContent).toContain('October 1, 2026 → November 1, 2026');
    expect(dialog.textContent).toContain('no end date → December 31, 2026');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    // Berlin: November 1 starts at 23:00 UTC the day before (winter time), December 31 ends at 23:00 UTC.
    expect((put.mock.calls[0]?.[1] as MandateUpdate).draft).toMatchObject({ valid_from: '2026-10-31T23:00:00Z', expires: '2026-12-31T23:00:00Z' });
  });
});

describe('saving', () => {
  it('shows what changes for the agent, stores a version and resets the counter', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(dialog.textContent).toContain('Applies to \u2068Sprachassistent\u2069 immediately. This creates version 2');
    expect(within(dialog).getByText('Changed')).toBeTruthy();
    expect(within(dialog).getByText(/Newly needs approval · 8/)).toBeTruthy();
    expect(dialog.textContent).toContain('Küchenlicht · turn on (Allowed → Ask first)');
    await tick();
    expect(document.activeElement).toBe(within(dialog).getByRole('button', { name: 'Keep editing' }));

    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(status()).toBe('All saved');
    expect(screen.getByRole('button', { name: /^Mandate version v2 · mock-/ })).toBeTruthy();
    expect(toastTexts()).toContain('Mandate saved · version 2');
    expect((await api.mandate(ID)).document.rules[0]?.decision).toBe('ask');
  });

  it('keeps editing on "Keep editing" and opens the summary with Ctrl+S', async () => {
    const { api } = await start();
    const put = vi.spyOn(api, 'putMandate');
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await fireEvent.keyDown(window, { key: 's', ctrlKey: true });
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(put).not.toHaveBeenCalled();
    expect(status()).toBe('1 unsaved change');
  });

  it('does nothing on Ctrl+S without changes', async () => {
    await start();
    await rulesRegion();
    await fireEvent.keyDown(window, { key: 'S', metaKey: true });
    await tick();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps the edit and says so when saving fails', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    api.putMandate = async () => {
      throw new ApiError('unavailable', 0);
    };
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    expect(await within(dialog).findByText('Couldn’t save. Your changes are still here.')).toBeTruthy();
    api.putMandate = async () => {
      throw new ApiError('invalid_mandate', 422, '/draft/rules/0');
    };
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    expect(await within(dialog).findByText('The server rejected the mandate. Please check the entries.')).toBeTruthy();
    expect(status()).toBe('1 unsaved change');
  });

  it('warns in the summary when the mandate applies for longer', async () => {
    await start({}, async (api) => void (await store(api, (d) => ({ ...d, expires: '2026-10-01T22:00:00Z' }))));
    expect(await screen.findByText('Expired')).toBeTruthy();
    const until = screen.getByLabelText('Valid until (optional)');
    await fireEvent.input(until, { target: { value: '' } });
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(within(dialog).getByText('The mandate applies for longer than before: earlier start or later end.')).toBeTruthy();
    expect(dialog.textContent).toContain('October 1, 2026 → no end date');
  });

  it('tells the preview when the draft would not apply right now', async () => {
    await start();
    await rulesRegion();
    expect(screen.queryByText(/This mandate doesn’t apply right now/)).toBeNull();
    await fireEvent.input(screen.getByLabelText('Valid from'), { target: { value: '2027-01-01' } });
    expect(screen.getByText(/This mandate doesn’t apply right now \(Not yet valid\)/)).toBeTruthy();
  });

  it('does not claim "no effect" when the device list is missing', async () => {
    await start({ failures: { devices: 'unavailable' } });
    expect(await screen.findByText(/Couldn’t load the device list/)).toBeTruthy();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Deny' }));
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(within(dialog).getByText(/The effect can’t be computed right now/)).toBeTruthy();
    expect(within(dialog).queryByText('No effect on permissions.')).toBeNull();
  });

  it('asks before leaving the page with unsaved changes', async () => {
    await start();
    await rulesRegion();
    const clean = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(clean);
    expect(clean.defaultPrevented).toBe(false);
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    const dirty = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(dirty);
    expect(dirty.defaultPrevented).toBe(true);
  });
});

describe('changes from elsewhere', () => {
  const fewer = (draft: MandateDraft): MandateDraft => ({ ...draft, rules: draft.rules.slice(0, 4) });

  it('takes over a new version when nothing is unsaved', async () => {
    const { api } = await start();
    await rulesRegion();
    await store(api, fewer);
    await waitFor(async () => expect(await cards()).toHaveLength(5));
    expect(toastTexts()).toContain('The mandate was updated · version 2');
    expect(status()).toBe('All saved');
  });

  it('never overwrites silently: a conflict keeps the edit and offers both ways', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await store(api, fewer);
    const banner = (await screen.findByText('This mandate was changed in the meantime')).closest('[role="alert"]') as HTMLElement;
    expect(banner.textContent).toContain('Version 2 was saved in the meantime.');
    expect(await cards()).toHaveLength(6);

    await fireEvent.click(within(banner).getByRole('button', { name: 'Apply my changes to version 2' }));
    expect(screen.queryByText('This mandate was changed in the meantime')).toBeNull();
    // The edit now also brings the camera rule back, which version 2 does not have.
    expect(status()).toBe('2 unsaved changes');
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(within(dialog).getByText('Added')).toBeTruthy();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 3' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect((await api.mandate(ID)).versions).toHaveLength(3);
  });

  it('discards the edit on request', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await store(api, fewer);
    const banner = (await screen.findByText('This mandate was changed in the meantime')).closest('[role="alert"]') as HTMLElement;
    await fireEvent.click(within(banner).getByRole('button', { name: 'Discard my changes' }));
    expect(await cards()).toHaveLength(5);
    expect(status()).toBe('All saved');
  });

  it('finds the conflict when saving and does not store anything', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    // Someone else stores a version just before this save reaches the server.
    const put = api.putMandate.bind(api);
    api.putMandate = async (id, update) => {
      api.putMandate = put;
      await store(api, fewer);
      return put(id, update);
    };
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    expect(await screen.findByText('This mandate was changed in the meantime')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect((await api.mandate(ID)).versions).toHaveLength(2);
    expect(status()).toBe('1 unsaved change');
  });

  it('says so when a conflict cannot be explained because the reload fails', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    api.putMandate = async () => {
      throw new ApiError('conflict', 409);
    };
    api.mandate = async () => {
      throw new ApiError('unavailable', 0);
    };
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    await waitFor(() => expect(toasts.list().map((t) => [t.kind, t.text])).toContainEqual(['error', 'Couldn’t save. Your changes are still here.']));
    expect(status()).toBe('1 unsaved change');
  });

  it('does not bring back a confirmation for critical actions that someone else took back', async () => {
    const granted: Rule = { id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['read', 'unlock'], decision: 'allow', allow_critical: true };
    const grant = (d: MandateDraft): MandateDraft => ({ ...d, rules: d.rules.map((r) => (r.id === 'door' ? granted : r)) });
    const { api } = await start({}, async (a) => {
      const { document: doc, summary } = await a.mandate(ID);
      await a.putMandate(ID, { name: summary.name, draft: grant(draftOf(doc)), base_digest: summary.digest, confirm_critical: true });
    });
    const put = vi.spyOn(api, 'putMandate');
    // This edit changes something else and still carries the confirmed rule …
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    // … while someone else takes the confirmation back.
    await store(api, (d) => ({ ...d, rules: d.rules.map((r) => (r.id === 'door' ? { ...r, decision: 'ask' as const, allow_critical: undefined } : r)) }));
    put.mockClear();
    const banner = (await screen.findByText('This mandate was changed in the meantime')).closest('[role="alert"]') as HTMLElement;
    await fireEvent.click(within(banner).getByRole('button', { name: 'Apply my changes to version 3' }));
    await tick();
    expect(live()).toBe('The permission without approval was withdrawn because the rule changed.');
    await fireEvent.click(saveButton());
    // No critical grant comes along: an ordinary dialog and no confirm_critical.
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    expect(within(dialog).queryByText('Contains critical actions without approval')).toBeNull();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 4' }));
    await waitFor(() => expect(put).toHaveBeenCalled());
    const update = put.mock.calls[0]?.[1] as MandateUpdate;
    expect(update.confirm_critical).toBeUndefined();
    expect(update.draft.rules.find((r) => r.id === 'door')).toEqual({ id: 'door', resource: { entity_id: 'lock.front_door' }, actions: ['read', 'unlock'], decision: 'allow' });
  });

  it('drops the conflict once there is nothing left to lose', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await store(api, fewer);
    await screen.findByText('This mandate was changed in the meantime');
    await fireEvent.click(first.getByRole('radio', { name: 'Allow' }));
    await waitFor(() => expect(screen.queryByText('This mandate was changed in the meantime')).toBeNull());
    expect(await cards()).toHaveLength(5);
  });

  it('catches up on a change that arrived while a save was running', async () => {
    const { api } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await fireEvent.click(saveButton());
    const dialog = await screen.findByRole('dialog', { name: 'Save changes?' });
    const put = api.putMandate.bind(api);
    const approvers = vi.spyOn(api, 'approvers');
    api.putMandate = async (id, update) => {
      api.control.emit({ type: 'approvers.changed' });
      approvers.mockClear();
      return put(id, update);
    };
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Save as version 2' }));
    await waitFor(() => expect(approvers).toHaveBeenCalled());
  });

  it('follows a rename without a new version', async () => {
    const { api } = await start();
    await rulesRegion();
    await store(api, (d) => d, 'Neuer Name');
    await waitFor(() => expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Neuer Name'));
    expect(status()).toBe('All saved');
  });

  it('keeps an unsaved edit when the page is left and opened again', async () => {
    const { app, view } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await tick();
    view.unmount();
    expect(app.unsaved.has(ID)).toBe(true);
    render(MandateEditor, { app, id: ID, now: Date.parse(NOW) });
    await waitFor(() => expect(status()).toBe('1 unsaved change'));
    expect((await card(1)).getByText('Ask first')).toBeTruthy();
  });

  it('forgets the kept edit once it is saved or undone', async () => {
    const { app } = await start();
    const first = await open(1);
    await fireEvent.click(first.getByRole('radio', { name: 'Ask' }));
    await tick();
    expect(app.unsaved.has(ID)).toBe(true);
    await fireEvent.click(first.getByRole('radio', { name: 'Allow' }));
    await tick();
    expect(app.unsaved.has(ID)).toBe(false);
  });
});

describe('states', () => {
  it('shows a revoked mandate read-only', async () => {
    await start({}, async (api) => void (await api.revokeMandate(ID)));
    await rulesRegion();
    expect(screen.getByText('Revoked')).toBeTruthy();
    expect(screen.getByText(/This mandate is revoked and no longer applies/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Save …' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Add rule' })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Edit/ })).toBeNull();
    expect(screen.queryByRole('button', { name: /^Move rule/ })).toBeNull();
    expect((screen.getByLabelText('Display name') as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByRole('link', { name: 'Versions' })).toBeTruthy();
  });

  it('says that mandates keep applying when loading fails, and recovers', async () => {
    const { api } = await start({ failures: { mandate: 'unavailable' } });
    const alert = await screen.findByRole('alert');
    expect(within(alert).getByText('Couldn’t load the mandate')).toBeTruthy();
    expect(within(alert).getByText('Existing mandates keep applying unchanged.')).toBeTruthy();
    const fresh = createMockClient();
    api.mandate = fresh.mandate;
    await fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
    expect((await screen.findByRole('heading', { level: 1 })).textContent).toBe('Sprachassistent Küche');
  });

  it('says so when the mandate does not exist', async () => {
    await start({}, undefined, 'mandate-gone');
    expect(await screen.findByRole('heading', { name: 'This mandate doesn’t exist' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'All mandates' }).getAttribute('href')).toBe('#/mandates');
  });

  it('shows a skeleton while loading', async () => {
    const api = createMockClient();
    const app = new AppState(api, () => Date.parse(NOW));
    await app.start();
    api.mandate = () => new Promise(() => {});
    render(MandateEditor, { app, id: ID, now: Date.parse(NOW) });
    expect(screen.getByRole('status', { name: 'Loading …' })).toBeTruthy();
  });

  it('stays usable without the device catalog and the approvers', async () => {
    await start({ failures: { devices: 'unavailable', approvers: 'unavailable' } });
    const list = await cards();
    expect(list[2]?.textContent).toContain('lock.front_door: read, unlock');
    expect(within(list[0] as HTMLElement).getByText('Matches no device right now.')).toBeTruthy();
    // The approver named in the mandate is still offered, by its id.
    expect(screen.getByRole('button', { name: /u-admin/ }).getAttribute('aria-pressed')).toBe('true');
  });

  it('cannot edit a rule on an extension category, only move or delete it', async () => {
    const extension: Rule = { id: 'docs', resource: { category: 'paperless:document' }, actions: ['read'], decision: 'allow' };
    await start({}, async (api) => void (await store(api, (d) => ({ ...d, rules: [extension, ...d.rules] }))));
    const first = await card(1);
    expect(first.getByText(/This rule uses an extension category/)).toBeTruthy();
    expect(first.queryByRole('button', { name: 'Edit: Rule 1' })).toBeNull();
    expect(first.getByRole('button', { name: 'Move rule 1 down' })).toBeTruthy();
    await fireEvent.click(first.getByRole('button', { name: 'Delete: Rule 1' }));
    expect((await cards())[0]?.textContent).toContain('Lights');
  });

  it('offers a search from ten rules on; the open rule stays visible', async () => {
    const many = (d: MandateDraft): MandateDraft => ({
      ...d,
      rules: [...d.rules, ...Array.from({ length: 6 }, (_, i): Rule => ({ id: `extra-${i}`, resource: { category: 'switch' }, actions: ['read'], decision: 'deny' }))],
    });
    await start({}, async (api) => void (await store(api, many)));
    expect(await cards()).toHaveLength(12);
    await open(1);
    await fireEvent.input(screen.getByLabelText('Search rules'), { target: { value: 'haust' } });
    const found = await cards();
    expect(found).toHaveLength(3);
    expect(found[0]?.textContent).toContain('Lights');
    expect(found[1]?.textContent).toContain('Rule 3: Haustür');
    await fireEvent.input(screen.getByLabelText('Search rules'), { target: { value: 'nothing like this' } });
    await fireEvent.click((await card(1)).getByRole('button', { name: 'Done' }));
    expect(screen.getByText('No rule matches the search.')).toBeTruthy();
  });

  it('shows rules and preview behind tabs on narrow screens', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }));
    await start();
    const tabs = await screen.findByRole('tablist');
    const [rules, preview] = within(tabs).getAllByRole('tab');
    expect(rules?.getAttribute('aria-selected')).toBe('true');
    expect(screen.getByRole('tabpanel', { name: 'Rules' })).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Preview: what it may do' })).toBeNull();
    await fireEvent.keyDown(rules as HTMLElement, { key: 'ArrowRight' });
    expect(preview?.getAttribute('aria-selected')).toBe('true');
    expect(document.activeElement).toBe(preview);
    expect(within(screen.getByRole('tabpanel', { name: 'Preview' })).getByRole('region', { name: 'Preview: what it may do' })).toBeTruthy();
    // No grid on mobile: cards per device.
    expect(screen.queryByRole('grid')).toBeNull();
    await fireEvent.click(rules as HTMLElement);
    expect(screen.getByRole('tabpanel', { name: 'Rules' })).toBeTruthy();
  });
});
