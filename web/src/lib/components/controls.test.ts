// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { setLocale } from '../paraglide/runtime.js';
import Button from './Button.svelte';
import DecisionSegment from './DecisionSegment.svelte';
import IconButton from './IconButton.svelte';
import SelectField from './SelectField.svelte';
import Switch from './Switch.svelte';
import TextField from './TextField.svelte';
import ToggleChip from './ToggleChip.svelte';
import { text } from './test-snippets.ts';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('Button', () => {
  it('runs onclick and renders its variant', async () => {
    const onclick = vi.fn();
    render(Button, { variant: 'primary', onclick, children: text('Save') });
    const button = screen.getByRole('button', { name: 'Save' });
    expect(button.className).toContain('primary');
    await fireEvent.click(button);
    expect(onclick).toHaveBeenCalledOnce();
  });

  it('stays focusable but ignores clicks while disabled', async () => {
    const onclick = vi.fn();
    render(Button, { disabled: true, onclick, children: text('Revoke') });
    const button = screen.getByRole('button', { name: 'Revoke' });
    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect(button.hasAttribute('disabled')).toBe(false);
    await fireEvent.click(button);
    expect(onclick).not.toHaveBeenCalled();
  });

  it('does not submit its form while disabled', async () => {
    const form = document.createElement('form');
    const submit = vi.fn((e: Event) => e.preventDefault());
    form.addEventListener('submit', submit);
    document.body.append(form);
    render(Button, { props: { type: 'submit', disabled: true, children: text('Save') }, target: form });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(submit).not.toHaveBeenCalled();
    form.remove();
  });

  it('keeps the class a caller adds', () => {
    render(Button, { class: 'wide', children: text('Save') });
    expect(screen.getByRole('button').className).toContain('wide');
  });

  it('shows loading and ignores clicks while busy', async () => {
    const onclick = vi.fn();
    render(Button, { busy: true, onclick, children: text('Save') });
    const button = screen.getByRole('button');
    expect(button.getAttribute('aria-busy')).toBe('true');
    expect(button.textContent).toContain('Loading');
    await fireEvent.click(button);
    expect(onclick).not.toHaveBeenCalled();
  });
});

describe('IconButton', () => {
  it('has an accessible name and shows the tooltip only after 600 ms of focus', async () => {
    vi.useFakeTimers();
    const { container } = render(IconButton, { icon: 'close', label: 'Close' });
    const button = screen.getByRole('button', { name: 'Close' });
    await fireEvent.focus(button);
    vi.advanceTimersByTime(599);
    await Promise.resolve();
    expect(container.querySelector('.tip')).toBeNull();
    vi.advanceTimersByTime(1);
    await vi.waitFor(() => expect(container.querySelector('.tip')?.textContent).toBe('Close'));
    await fireEvent.blur(button);
    await vi.waitFor(() => expect(container.querySelector('.tip')).toBeNull());
  });

  it('hides the tooltip on Escape', async () => {
    vi.useFakeTimers();
    const { container } = render(IconButton, { icon: 'dots', label: 'More' });
    const button = screen.getByRole('button', { name: 'More' });
    await fireEvent.pointerEnter(button.parentElement as HTMLElement);
    vi.advanceTimersByTime(600);
    await vi.waitFor(() => expect(container.querySelector('.tip')).not.toBeNull());
    await fireEvent.keyDown(button, { key: 'Escape' });
    await vi.waitFor(() => expect(container.querySelector('.tip')).toBeNull());
  });
});

describe('TextField', () => {
  it('labels the input and wires help text', () => {
    render(TextField, { label: 'Name', value: 'Kitchen', help: 'Only for you' });
    const input = screen.getByLabelText('Name') as HTMLInputElement;
    expect(input.value).toBe('Kitchen');
    expect(input.getAttribute('aria-invalid')).toBeNull();
    expect(document.getElementById(input.getAttribute('aria-describedby') ?? '')?.textContent).toBe('Only for you');
  });

  it('shows the error instead of the help and marks the input invalid', () => {
    render(TextField, { label: 'Name', value: '', help: 'Only for you', error: 'Enter a name.' });
    const input = screen.getByLabelText('Name');
    expect(input.getAttribute('aria-invalid')).toBe('true');
    expect(document.getElementById(input.getAttribute('aria-describedby') ?? '')?.textContent).toBe('Enter a name.');
    expect(screen.queryByText('Only for you')).toBeNull();
  });
});

describe('SelectField', () => {
  it('offers the options and reports the choice', async () => {
    const onchange = vi.fn();
    render(SelectField, {
      label: 'Area',
      value: '',
      options: [
        { value: '', label: 'All areas' },
        { value: 'kitchen', label: 'Küche' },
      ],
      onchange,
    });
    const select = screen.getByLabelText('Area') as HTMLSelectElement;
    expect([...select.options].map((o) => o.textContent)).toEqual(['All areas', 'Küche']);
    await fireEvent.change(select, { target: { value: 'kitchen' } });
    expect(onchange).toHaveBeenCalledWith('kitchen');
  });
});

describe('DecisionSegment', () => {
  it('is a radio group with one tab stop on the selected decision and its description', () => {
    render(DecisionSegment, { value: 'ask' });
    const group = screen.getByRole('radiogroup', { name: 'Decision' });
    const radios = screen.getAllByRole('radio');
    expect(radios.map((r) => r.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false']);
    expect(radios.map((r) => r.tabIndex)).toEqual([-1, 0, -1]);
    expect(document.getElementById(group.getAttribute('aria-describedby') ?? '')?.textContent).toMatch(/phone/);
  });

  it('moves and selects with the arrow keys, wrapping around', async () => {
    const onchange = vi.fn();
    render(DecisionSegment, { value: 'ask', onchange });
    const [allow, ask, deny] = screen.getAllByRole('radio') as [HTMLElement, HTMLElement, HTMLElement];
    await fireEvent.keyDown(ask, { key: 'ArrowRight' });
    expect(onchange).toHaveBeenLastCalledWith('deny');
    expect(document.activeElement).toBe(deny);
    await fireEvent.keyDown(deny, { key: 'ArrowDown' });
    expect(onchange).toHaveBeenLastCalledWith('allow');
    expect(document.activeElement).toBe(allow);
    await fireEvent.keyDown(allow, { key: 'ArrowUp' });
    expect(onchange).toHaveBeenLastCalledWith('deny');
    await fireEvent.keyDown(deny, { key: 'Tab' });
    expect(onchange).toHaveBeenCalledTimes(3);
  });

  it('mirrors left and right in right-to-left layouts', async () => {
    const onchange = vi.fn();
    const { container } = render(DecisionSegment, { value: 'ask', onchange });
    container.setAttribute('dir', 'rtl');
    (container as HTMLElement).style.direction = 'rtl';
    const ask = screen.getAllByRole('radio')[1] as HTMLElement;
    await fireEvent.keyDown(ask, { key: 'ArrowLeft' });
    expect(onchange).toHaveBeenLastCalledWith('deny');
  });

  it('selects on click', async () => {
    const onchange = vi.fn();
    render(DecisionSegment, { value: 'ask', onchange });
    await fireEvent.click(screen.getByRole('radio', { name: /Allow/ }));
    expect(onchange).toHaveBeenCalledWith('allow');
    await fireEvent.click(screen.getByRole('radio', { name: /Allow/ }));
    expect(onchange).toHaveBeenCalledOnce();
  });
});

describe('Switch and ToggleChip', () => {
  it('switches with role switch and a description', async () => {
    const onchange = vi.fn();
    render(Switch, { checked: false, label: 'Allow critical actions', description: 'Off', tone: 'danger', onchange });
    const sw = screen.getByRole('switch', { name: 'Allow critical actions' });
    expect(sw.getAttribute('aria-checked')).toBe('false');
    await fireEvent.click(sw);
    expect(sw.getAttribute('aria-checked')).toBe('true');
    expect(onchange).toHaveBeenCalledWith(true);
  });

  it('toggles a chip with aria-pressed and marks critical actions', async () => {
    const onchange = vi.fn();
    render(ToggleChip, { pressed: false, critical: true, onchange, children: text('unlock') });
    const chip = screen.getByRole('button', { name: /unlock/ });
    expect(chip.getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByRole('img', { name: 'Critical' })).toBeTruthy();
    await fireEvent.click(chip);
    expect(chip.getAttribute('aria-pressed')).toBe('true');
    expect(onchange).toHaveBeenCalledWith(true);
  });
});
