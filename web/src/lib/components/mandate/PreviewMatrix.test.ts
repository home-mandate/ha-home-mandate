// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { devicesFixture, HOSTILE_NAME, voiceAssistantDraft } from '../../api/fixtures.ts';
import type { Device, DeviceCatalog, MandateDraft, Rule } from '../../api/types.ts';
import { removeRule, replaceRule, withDecision } from '../../mandate/edit.ts';
import { setLocale } from '../../paraglide/runtime.js';
import PreviewMatrix from './PreviewMatrix.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  document.documentElement.removeAttribute('dir');
});

const base = voiceAssistantDraft;

function show(draft: MandateDraft = base, props: Partial<{ grid: boolean; invalid: boolean; catalog: DeviceCatalog; catalogMissing: boolean; notInEffect: string }> = {}) {
  return render(PreviewMatrix, { draft, previous: base, version: 4, catalog: devicesFixture, locale: 'en', grid: true, invalid: false, ...props });
}

const group = (name: RegExp) => screen.getByRole('button', { name });
const cell = (name: RegExp) => screen.getByRole('gridcell', { name });

describe('PreviewMatrix on desktop', () => {
  it('counts the outcomes and shows the groups with their counts, the first two open', () => {
    show();
    const region = screen.getByRole('region', { name: 'Preview: what it may do' });
    expect(within(region).getByText('10 devices:')).toBeTruthy();
    expect(region.querySelector('.tally')?.textContent?.replace(/\s+/g, ' ')).toContain('10 devices: 10 Allowed2 Ask first2 Denied18 Default: denied');
    const lights = group(/^Lights/);
    expect(lights.getAttribute('aria-expanded')).toBe('true');
    expect(lights.textContent).toContain('2 devices');
    expect(within(lights).getByRole('img', { name: 'Allowed' })).toBeTruthy();
    expect(group(/^Climate/).getAttribute('aria-expanded')).toBe('true');
    expect(group(/^Lock/).getAttribute('aria-expanded')).toBe('false');
    expect(screen.getAllByRole('grid').map((g) => g.getAttribute('aria-label'))).toEqual(['Lights', 'Climate']);
  });

  it('has a grid per block with column and row headers; critical actions carry the shield', async () => {
    show();
    await fireEvent.click(group(/^Lock/));
    const grid = screen.getByRole('grid', { name: 'Lock' });
    expect(within(grid).getAllByRole('columnheader').map((h) => h.textContent?.trim())).toEqual(['Device', 'read', 'lock', 'unlock', 'open']);
    expect(within(within(grid).getAllByRole('columnheader')[3] as HTMLElement).getByRole('img', { name: 'Critical' })).toBeTruthy();
    expect(within(grid).getByRole('rowheader').textContent).toContain('Haustür');
    expect(within(grid).getByRole('rowheader').textContent).toContain('lock.front_door');
    expect(within(grid).getAllByRole('gridcell').map((c) => c.textContent?.trim())).toEqual(['Ask first', 'Default', 'Ask first', 'Default']);
  });

  it('explains the selected cell and marks it', async () => {
    show();
    expect(screen.getByText('Select a cell to see which rule decides.')).toBeTruthy();
    const turnOn = cell(/Küchenlicht.*turn on: Allowed\. From Rule 1/);
    await fireEvent.click(turnOn);
    expect(turnOn.getAttribute('aria-selected')).toBe('true');
    const detail = document.querySelector('.detail') as HTMLElement;
    expect(detail.textContent).toContain('Küchenlicht · turn on');
    // Only the reason is announced; the cell's name already says device, action and decision.
    const announced = detail.querySelector('[aria-live="polite"]') as HTMLElement;
    expect(announced.getAttribute('aria-atomic')).toBe('true');
    expect(announced.textContent).toMatch(/^Rule 1: .Lights.: read, turn on, turn off, adjust → Allowed · 1 rule applies$/);
    expect(announced.textContent).not.toContain('Küchenlicht');
  });

  it('keeps one cell per grid in the tab order and moves with the arrow keys; the selection follows', async () => {
    show();
    const grid = screen.getByRole('grid', { name: 'Lights' });
    const cells = within(grid).getAllByRole('gridcell');
    expect(cells.filter((c) => c.tabIndex === 0)).toEqual([cells[0]]);
    (cells[0] as HTMLElement).focus();
    await fireEvent.keyDown(cells[0] as HTMLElement, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(cells[1]);
    expect(cells[1]?.getAttribute('aria-selected')).toBe('true');
    expect(within(grid).getAllByRole('gridcell').filter((c) => c.tabIndex === 0)).toEqual([cells[1]]);
    await fireEvent.keyDown(cells[1] as HTMLElement, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(cells[5]);
    await fireEvent.keyDown(cells[5] as HTMLElement, { key: 'Home' });
    expect(document.activeElement).toBe(cells[4]);
    await fireEvent.keyDown(cells[4] as HTMLElement, { key: 'End', ctrlKey: true });
    expect(document.activeElement).toBe(cells[7]);
    await fireEvent.keyDown(cells[7] as HTMLElement, { key: 'Home', ctrlKey: true });
    expect(document.activeElement).toBe(cells[0]);
    // Other keys are left alone.
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    cells[0]?.dispatchEvent(enter);
    expect(enter.defaultPrevented).toBe(false);
  });

  it('mirrors the horizontal arrows right-to-left', async () => {
    document.documentElement.setAttribute('dir', 'rtl');
    show();
    const cells = within(screen.getByRole('grid', { name: 'Lights' })).getAllByRole('gridcell');
    (cells[1] as HTMLElement).focus();
    await fireEvent.keyDown(cells[1] as HTMLElement, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(cells[2]);
  });

  it('marks downgraded, time-dependent and changed cells, never by colour alone', async () => {
    const draft = removeRule(replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow')), 4);
    show(draft);
    await fireEvent.click(group(/^Lock/));
    const unlock = cell(/Haustür.*unlock: Ask first\. From Rule 3\. Downgraded/);
    expect(unlock.textContent?.trim()).toBe('Ask first');
    expect(cell(/Haustür.*read: Allowed\. From Rule 3\. Changed since the previous version/).querySelector('.dot')).not.toBeNull();
    await fireEvent.click(unlock);
    expect(document.querySelector('[aria-live="polite"]')?.textContent).toContain('Downgraded: Becomes an approval request because the action is critical.');
    await fireEvent.click(group(/^Media/));
    expect(cell(/Lautsprecher.*play: Default: denied, at times Allowed/)).toBeTruthy();
    const legend = document.querySelector('.legend')?.textContent ?? '';
    expect(legend).toContain('Downgraded');
    expect(legend).toContain('Time-dependent');
    expect(legend).toContain('Changed since the previous version');
  });

  it('groups by area with one grid per category', async () => {
    show();
    const radios = screen.getAllByRole('radio');
    expect(radios.map((r) => [r.textContent?.trim(), r.getAttribute('aria-checked')])).toEqual([
      ['By area', 'false'],
      ['By category', 'true'],
    ]);
    await fireEvent.keyDown(radios[1] as HTMLElement, { key: 'ArrowRight' });
    expect(radios[0]?.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(radios[0]);
    expect(group(/^Küche/).getAttribute('aria-expanded')).toBe('true');
    expect(group(/^No area/).textContent).toContain('3 devices');
    expect(screen.getAllByRole('grid').map((g) => g.getAttribute('aria-label'))).toEqual(['Küche · Lights', 'Wohnzimmer · Lights', 'Wohnzimmer · Climate', 'Wohnzimmer · Media']);
    await fireEvent.click(radios[1] as HTMLElement);
    expect(group(/^Lights/)).toBeTruthy();
  });

  it('filters by text and opens every matching group', async () => {
    show();
    await fireEvent.input(screen.getByRole('searchbox', { name: 'Filter devices' }), { target: { value: 'garage' } });
    expect(screen.getAllByRole('grid').map((g) => g.getAttribute('aria-label'))).toEqual(['Gate/garage', 'Camera']);
    expect(screen.getAllByRole('status').map((s) => s.textContent)).toContain('2 devices');
    await fireEvent.input(screen.getByRole('searchbox'), { target: { value: 'nothing like this' } });
    // Said to everyone: visibly, and through the status for screen readers.
    expect(screen.getAllByText('No device matches the filter.')).toHaveLength(2);
    expect(screen.queryByRole('grid')).toBeNull();
  });

  it('reduces the preview to what changed', async () => {
    show(removeRule(base, 4));
    await fireEvent.click(screen.getByRole('switch', { name: 'Only changes vs. v4' }));
    expect(screen.getAllByRole('grid').map((g) => g.getAttribute('aria-label'))).toEqual(['Camera']);
    await fireEvent.click(cell(/Kamera Einfahrt.*get snapshot/));
    expect(document.querySelector('[aria-live="polite"]')?.textContent).toContain('Changed since the previous version · v4: Denied');
  });

  it('closes and opens groups by hand', async () => {
    show();
    await fireEvent.click(group(/^Lights/));
    expect(group(/^Lights/).getAttribute('aria-expanded')).toBe('false');
    expect(screen.getAllByRole('grid').map((g) => g.getAttribute('aria-label'))).toEqual(['Climate']);
    await fireEvent.click(group(/^Sensor/));
    expect(screen.getAllByRole('grid').map((g) => g.getAttribute('aria-label'))).toEqual(['Climate', 'Sensor']);
  });

  it('shows five rows of a block and the rest on request', async () => {
    const lights = Array.from({ length: 8 }, (_, i): Device => ({ entity_id: `light.l${i}`, name: `Light ${i}`, category: 'light', area: null, actions: ['read'] }));
    show(base, { catalog: { areas: [], devices: lights } });
    const grid = () => screen.getByRole('grid', { name: 'Lights' });
    expect(within(grid()).getAllByRole('rowheader')).toHaveLength(5);
    await fireEvent.click(screen.getByRole('button', { name: 'Show 3 more' }));
    expect(within(grid()).getAllByRole('rowheader')).toHaveLength(8);
    expect(screen.queryByRole('button', { name: /^Show/ })).toBeNull();
  });

  it('leaves a hole where a device lacks an action of its block', () => {
    const devices: Device[] = [
      { entity_id: 'light.a', name: 'A', category: 'light', area: null, actions: ['read', 'turn_on'] },
      { entity_id: 'light.b', name: 'B', category: 'light', area: null, actions: ['read'] },
    ];
    show(base, { catalog: { areas: [], devices } });
    const cells = within(screen.getByRole('grid', { name: 'Lights' })).getAllByRole('gridcell');
    expect(cells).toHaveLength(4);
    expect(cells[3]?.tagName).toBe('DIV');
    expect(cells[3]?.getAttribute('aria-label')).toBe('Not available');
    // Moving down from "turn on" of A finds no cell and stays.
    (cells[1] as HTMLElement).focus();
    fireEvent.keyDown(cells[1] as HTMLElement, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(cells[1]);
  });

  it('steps over a hole with the arrows and stops before it with End', () => {
    const devices: Device[] = [{ entity_id: 'light.a', name: 'A', category: 'light', area: null, actions: ['read', 'turn_off', 'set'] }];
    const full: Device = { entity_id: 'light.b', name: 'B', category: 'light', area: null, actions: ['read', 'turn_on', 'turn_off'] };
    show(base, { catalog: { areas: [], devices: [...devices, full] } });
    const row = (n: number) => within(screen.getAllByRole('row')[n] as HTMLElement).getAllByRole('gridcell');
    const a = row(1);
    // A has no "turn on" (column 2 of 4): the arrow goes on to "turn off".
    (a[0] as HTMLElement).focus();
    fireEvent.keyDown(a[0] as HTMLElement, { key: 'ArrowRight' });
    expect(document.activeElement).toBe(a[2]);
    fireEvent.keyDown(a[2] as HTMLElement, { key: 'ArrowLeft' });
    expect(document.activeElement).toBe(a[0]);
    // B has no "adjust" (last column): End stops at the last cell that exists.
    const b = row(2);
    (b[0] as HTMLElement).focus();
    fireEvent.keyDown(b[0] as HTMLElement, { key: 'End' });
    expect(document.activeElement).toBe(b[2]);
  });

  it('shows hostile device names as text', async () => {
    show();
    await fireEvent.click(group(/^Script/));
    expect(within(screen.getByRole('grid', { name: 'Script' })).getByRole('rowheader').textContent).toContain(HOSTILE_NAME);
    expect(document.querySelector('img:not([role])')).toBeNull();
  });

  it('says so when the draft has errors, there are no devices, or the device list is missing', () => {
    show(base, { invalid: true });
    expect(screen.getByText(/The draft has errors/)).toBeTruthy();
    cleanup();
    show(base, { catalog: { areas: [], devices: [] } });
    expect(screen.getByText(/Home Assistant reported no devices/)).toBeTruthy();
    cleanup();
    show(base, { catalog: { areas: [], devices: [] }, catalogMissing: true });
    expect(screen.getByText(/Couldn’t load the device list/)).toBeTruthy();
    expect(screen.queryByText(/Home Assistant reported no devices/)).toBeNull();
  });

  it('says when the mandate does not apply right now, so green cells are not read as "in effect"', () => {
    show(base, { notInEffect: 'Expired' });
    expect(screen.getByText('This mandate doesn’t apply right now (Expired). The preview shows what applies while it is valid.')).toBeTruthy();
  });

  it('names the grouping choice and makes the groups headings', () => {
    show();
    expect(screen.getByRole('radiogroup', { name: 'Group by' })).toBeTruthy();
    expect(screen.getAllByRole('heading', { level: 3 }).length).toBe(9);
  });
});

describe('PreviewMatrix on mobile', () => {
  it('has no grid: one card per device with a list "action → decision" and the reason', async () => {
    const draft = replaceRule(base, 2, withDecision(base.rules[2] as Rule, 'allow'));
    show(draft, { grid: false });
    expect(screen.queryByRole('grid')).toBeNull();
    expect(screen.queryByText('Select a cell to see which rule decides.')).toBeNull();
    await fireEvent.click(group(/^Lock/));
    const card = screen.getByText('lock.front_door').closest('li') as HTMLElement;
    const lines = within(card).getAllByRole('listitem').map((li) => li.textContent?.replace(/\s+/g, ' ').trim());
    expect(lines).toEqual([
      'read Allowed From Rule 3',
      'lock Default: denied No rule applies',
      'unlock Ask first From Rule 3 · Downgraded',
      'open Default: denied No rule applies',
    ]);
    expect(within(card).getByRole('img', { name: 'Changed since the previous version' })).toBeTruthy();
  });

  it('names the category of each block when grouped by area, and what applies at times', async () => {
    show(base, { grid: false });
    await fireEvent.click(screen.getByRole('radio', { name: 'By area' }));
    // The first two areas are open: kitchen and living room.
    expect(screen.getAllByRole('heading', { level: 4 }).map((h) => h.textContent)).toEqual(['Lights', 'Lights', 'Climate', 'Media']);
    const speaker = screen.getByText('media_player.living_room').closest('li') as HTMLElement;
    expect(speaker.textContent?.replace(/\s+/g, ' ')).toContain('play Default: denied No rule applies · Time-dependent: Allowed');
  });
});
