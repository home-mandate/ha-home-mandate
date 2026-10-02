// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { setLocale } from '../paraglide/runtime.js';
import { HOSTILE_NAME, HOSTILE_REASON } from '../api/fixtures.ts';
import { createToasts } from '../ui/toasts.ts';
import AgentName from './AgentName.svelte';
import Banner from './Banner.svelte';
import CopyField from './CopyField.svelte';
import DecisionBadge from './DecisionBadge.svelte';
import Dialog from './Dialog.svelte';
import EmptyState from './EmptyState.svelte';
import ErrorState from './ErrorState.svelte';
import ReasonBox from './ReasonBox.svelte';
import Skeleton from './Skeleton.svelte';
import StatusPill from './StatusPill.svelte';
import ToastHost from './ToastHost.svelte';
import { html, text } from './test-snippets.ts';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  document.body.replaceChildren();
});

describe('DecisionBadge and StatusPill', () => {
  it.each([
    ['allow', 'Allowed'],
    ['ask', 'Ask first'],
    ['deny', 'Denied'],
    ['default', 'Default: denied'],
    ['critical', 'Critical'],
  ] as const)('labels %s with icon and word', (kind, word) => {
    const { container } = render(DecisionBadge, { kind });
    const badge = container.querySelector('.badge') as HTMLElement;
    expect(badge.className).toContain(kind);
    expect(badge.textContent).toBe(word);
    expect(badge.querySelector('svg')).not.toBeNull();
  });

  it('draws the default decision with its own dashed icon', () => {
    const { container } = render(DecisionBadge, { kind: 'default' });
    expect(container.querySelector('circle')?.getAttribute('stroke-dasharray')).toBeTruthy();
  });

  it('renders a pill with a shaped mark', () => {
    const { container } = render(StatusPill, { tone: 'warning', shape: 'ring', children: text('Disconnected') });
    expect(container.querySelector('.pill.warning')?.textContent).toBe('Disconnected');
    expect(container.querySelector('.mark.ring')?.getAttribute('aria-hidden')).toBe('true');
  });
});

describe('Banner', () => {
  it('is a status for info and an alert otherwise, with an optional action', async () => {
    const onclick = vi.fn();
    render(Banner, { kind: 'info', body: 'Add an approver.' });
    expect(screen.getByRole('status').textContent).toContain('Add an approver.');
    cleanup();
    render(Banner, { kind: 'estop', title: 'Emergency stop active', body: 'Since 19:42', action: { label: 'Lift', onclick } });
    const alert = screen.getByRole('alert');
    expect(alert.className).toContain('estop');
    await fireEvent.click(screen.getByRole('button', { name: 'Lift' }));
    expect(onclick).toHaveBeenCalledOnce();
  });
});

describe('ToastHost', () => {
  it('shows toasts, runs their action and closes errors', async () => {
    const toasts = createToasts();
    const run = vi.fn();
    render(ToastHost, { toasts });
    toasts.show({ kind: 'undo', text: 'Rule deleted', action: { label: 'Undo', run } });
    toasts.show({ kind: 'error', text: 'Could not save' });
    await tick();
    expect(screen.getByRole('status').textContent).toContain('Rule deleted');
    expect(screen.getByRole('alert').textContent).toContain('Could not save');
    await fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    expect(run).toHaveBeenCalledOnce();
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await tick();
    // The live regions stay, so the next toast is announced; only the toasts are gone.
    expect(screen.getByRole('alert').textContent).toBe('');
    expect(screen.getByRole('status').textContent).toBe('');
  });

  it('pauses the timers while the pointer is on the toasts', async () => {
    vi.useFakeTimers();
    const toasts = createToasts();
    const { container } = render(ToastHost, { toasts });
    toasts.show({ kind: 'success', text: 'Saved' });
    await fireEvent.pointerEnter(container.querySelector('.host') as HTMLElement);
    vi.advanceTimersByTime(10_000);
    expect(toasts.list()).toHaveLength(1);
    await fireEvent.pointerLeave(container.querySelector('.host') as HTMLElement);
    vi.advanceTimersByTime(4000);
    expect(toasts.list()).toHaveLength(0);
  });
});

describe('Dialog', () => {
  const body = html('<div><h2 id="t">Revoke?</h2><button id="cancel">Cancel</button><button id="ok">Revoke</button></div>');

  it('focuses the first element, traps Tab, closes on Escape and restores focus', async () => {
    const trigger = document.createElement('button');
    document.body.append(trigger);
    trigger.focus();
    const onclose = vi.fn();
    const { rerender } = render(Dialog, { open: true, labelledby: 't', onclose, children: body });
    const dialog = screen.getByRole('dialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    await vi.waitFor(() => expect(document.activeElement?.id).toBe('cancel'));
    (document.getElementById('ok') as HTMLElement).focus();
    await fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(document.activeElement?.id).toBe('cancel');
    await fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onclose).toHaveBeenCalledOnce();
    await rerender({ open: false, labelledby: 't', onclose, children: body });
    expect(document.activeElement).toBe(trigger);
  });

  const backdrop = () => document.querySelector('.backdrop') as HTMLElement;

  it('closes on a backdrop click only when not destructive, and only if it started there', async () => {
    const onclose = vi.fn();
    const { rerender } = render(Dialog, { open: true, labelledby: 't', onclose, children: body });
    // A text selection that ends over the backdrop is no click on it.
    await fireEvent.pointerDown(screen.getByRole('dialog'));
    await fireEvent.click(backdrop());
    expect(onclose).not.toHaveBeenCalled();
    await fireEvent.pointerDown(backdrop());
    await fireEvent.click(backdrop());
    expect(onclose).toHaveBeenCalledOnce();
    await rerender({ open: true, labelledby: 't', destructive: true, onclose, children: body });
    expect(screen.getByRole('alertdialog')).toBeTruthy();
    await fireEvent.pointerDown(backdrop());
    await fireEvent.click(backdrop());
    expect(onclose).toHaveBeenCalledOnce();
  });

  it('lives in <body>, makes the app inert and brings lost focus back', async () => {
    const app = document.createElement('div');
    app.id = 'app';
    const outside = document.createElement('button');
    app.append(outside);
    document.body.append(app);
    const { rerender } = render(Dialog, { props: { open: true, labelledby: 't', onclose: () => {}, children: body }, target: app });
    const dialog = screen.getByRole('dialog');
    expect(dialog.closest('#app')).toBeNull();
    expect(app.inert).toBe(true);
    expect(document.documentElement.style.overflow).toBe('hidden');
    outside.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    await vi.waitFor(() => expect(document.activeElement?.id).toBe('cancel'));
    await rerender({ open: false, labelledby: 't', onclose: () => {}, children: body });
    expect(app.inert).toBe(false);
    expect(document.documentElement.style.overflow).toBe('');
  });

  it('falls back to <main> when the trigger is gone', async () => {
    const main = document.createElement('main');
    const trigger = document.createElement('button');
    document.body.append(main, trigger);
    trigger.focus();
    const { rerender } = render(Dialog, { open: true, labelledby: 't', onclose: () => {}, children: body });
    trigger.remove();
    await rerender({ open: false, labelledby: 't', onclose: () => {}, children: body });
    expect(document.activeElement).toBe(main);
  });

  it('leaves Escape to a tooltip that handled it first', async () => {
    const onclose = vi.fn();
    render(Dialog, { open: true, labelledby: 't', onclose, children: body });
    const event = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true, bubbles: true });
    event.preventDefault();
    window.dispatchEvent(event);
    expect(onclose).not.toHaveBeenCalled();
  });

  it('focuses the given safe button of a destructive dialog', async () => {
    const safe = document.createElement('button');
    const { container } = render(Dialog, { open: true, labelledby: 't', destructive: true, initial: safe, onclose: () => {}, children: body });
    container.append(safe);
    await tick();
    await tick();
    expect(document.activeElement).toBe(safe);
  });

  it('renders nothing while closed', () => {
    render(Dialog, { open: false, labelledby: 't', onclose: () => {}, children: body });
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('untrusted agent text', () => {
  it('renders a hostile name as text, isolated and marked as the agent claim', () => {
    const { container } = render(AgentName, { name: HOSTILE_NAME, client: 'https://agent.example/meta' });
    const bdi = container.querySelector('bdi') as HTMLElement;
    expect(bdi.textContent).toBe(HOSTILE_NAME);
    expect(bdi.getAttribute('title')).toBe('Stated by the agent, unverified');
    expect(container.querySelector('img, b')).toBeNull();
    expect(container.querySelector('.domain')?.textContent).toBe('agent.example');
  });

  it('marks the identifier of a paired agent as unverified, never as a domain', () => {
    const { container } = render(AgentName, { name: 'Tablet', client: 'accounts.google.com' });
    expect(container.querySelector('.domain')).toBeNull();
    expect(container.querySelector('.claimed')?.textContent).toContain('accounts.google.com');
    expect(container.querySelector('.claimed .tag')?.textContent).toBe('unverified');
  });

  it('strips bidi overrides and line breaks from the reason', () => {
    const { container } = render(ReasonBox, { reason: HOSTILE_REASON });
    const quote = container.querySelector('blockquote bdi') as HTMLElement;
    expect(quote.textContent).not.toMatch(/[\u202A-\u202E\n]/);
    expect(quote.textContent).toContain('[Link](javascript:alert(1))');
    expect(container.querySelector('a')).toBeNull();
    expect(container.querySelector('figcaption')?.textContent).toContain('unverified');
  });
});

describe('states', () => {
  it('shows an empty state with its one action', () => {
    render(EmptyState, { icon: 'agent', title: 'No agent yet', body: 'Agents can do nothing.', action: html('<button>Pair</button>') });
    expect(screen.getByRole('heading', { name: 'No agent yet' })).toBeTruthy();
    expect(screen.getAllByRole('button')).toHaveLength(1);
  });

  it('shows an error state as an alert with retry', async () => {
    const onretry = vi.fn();
    render(ErrorState, { title: 'Could not load', body: 'Existing mandates stay in force.', onretry });
    expect(screen.getByRole('alert').textContent).toContain('stay in force');
    await fireEvent.click(screen.getByRole('button', { name: /Retry|Try again/ }));
    expect(onretry).toHaveBeenCalledOnce();
  });

  it('draws skeleton lines hidden from assistive technology', () => {
    const { container } = render(Skeleton, { lines: ['70%', '40%'] });
    expect(container.querySelectorAll('.line')).toHaveLength(2);
    expect(container.querySelector('.skeleton')?.getAttribute('aria-hidden')).toBe('true');
  });
});

describe('CopyField', () => {
  it('copies, says so for 2 s, then returns', async () => {
    vi.useFakeTimers();
    const copy = vi.fn(async () => {});
    render(CopyField, { label: 'MCP endpoint', value: 'https://home.example:8765/mcp', copy });
    expect((screen.getByLabelText('MCP endpoint') as HTMLInputElement).readOnly).toBe(true);
    await fireEvent.click(screen.getByRole('button', { name: /Copy/ }));
    expect(copy).toHaveBeenCalledWith('https://home.example:8765/mcp');
    await vi.waitFor(() => expect(screen.getByRole('button').textContent).toContain('Copied'));
    vi.advanceTimersByTime(2000);
    await vi.waitFor(() => expect(screen.getByRole('button').textContent).toContain('Copy'));
  });

  it('selects the text and says so when the clipboard is blocked', async () => {
    const copy = vi.fn(async () => {
      throw new Error('denied');
    });
    render(CopyField, { label: 'URL', value: 'https://x', copy });
    await fireEvent.click(screen.getByRole('button'));
    await vi.waitFor(() => expect(screen.getByRole('status').textContent).toContain('Ctrl+C'));
    expect(screen.getByRole('button').textContent).not.toContain('Copied');
    const input = screen.getByLabelText('URL') as HTMLInputElement;
    expect(document.activeElement).toBe(input);
    expect(input.selectionEnd).toBe('https://x'.length);
  });
});
