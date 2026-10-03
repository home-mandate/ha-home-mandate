<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing step 1 (design README 6.2): the code the agent shows. States idle, incomplete
  (not sent, so it costs no attempt), wrong, expired, locked (5 wrong codes, until the
  server's lock time has passed) and failed (any other error). Errors are announced once
  (role=alert, a new element per error) and tied to the field. While locked the field is
  read-only but stays focusable, so its message can still be reached; the end of the lock
  is announced politely.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import type { ApiClient } from '../../api/client.ts';
  import type { PairingCandidate } from '../../api/types.ts';
  import { codeError, isComplete, normalizeCode, type CodeState } from '../../agents/pairing.ts';
  import { m } from '../../i18n.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    api: ApiClient;
    /** Browser clock, ticking; ends the lock. */
    now: number;
    /** Code to start with, e.g. after going back. */
    initial?: string;
    /** State to start with, e.g. "expired" when the code ran out in a later step. */
    initialState?: CodeState;
    /** End of a lock that a later step ran into (browser clock, ms). */
    initialLockedUntil?: number;
    /** Focus the field once shown (after a restart, so its message is heard). */
    focusField?: boolean;
    headingId: string;
    onfound: (code: string, candidate: PairingCandidate) => void;
  }

  let { api, now, initial = '', initialState = 'idle', initialLockedUntil = 0, focusField = false, headingId, onfound }: Props = $props();

  const id = $props.id();
  const MINUTE_MS = 60_000;
  // Longest input the field takes: 8 characters with a dash and some spaces.
  const MAX_INPUT = 12;

  let text = $state(untrack(() => initial));
  let status: CodeState = $state(untrack(() => initialState));
  let lockedUntil = $state(untrack(() => initialLockedUntil));
  /** Minutes of the lock when it began; the alert does not count down (no repeated announcements). */
  let lockMinutes = $state(untrack(() => Math.max(1, Math.ceil((initialLockedUntil - now) / MINUTE_MS))));
  let busy = $state(false);
  let errors = $state(0);
  let unlocked = $state(false);
  let field: HTMLInputElement | undefined = $state();

  const locked = $derived(status === 'locked' && now < lockedUntil);
  const bad = $derived(status !== 'idle');
  const message = $derived.by(() => {
    switch (status) {
      case 'incomplete':
        return m.pair_code_incomplete();
      case 'wrong':
        return m.pair_code_wrong();
      case 'expired':
        return m.pair_code_expired();
      case 'locked':
        return m.pair_code_locked({ minutes: lockMinutes });
      case 'failed':
        return m.pair_failed();
      default:
        return m.pair_code_help();
    }
  });

  // The lock ends by the clock: say so once.
  $effect(() => {
    if (status === 'locked' && !locked) {
      untrack(() => {
        status = 'idle';
        unlocked = true;
      });
    }
  });

  $effect(() => {
    if (field && untrack(() => focusField)) field.focus();
  });

  function typed() {
    unlocked = false;
    if (status !== 'locked') status = 'idle';
  }

  async function fail(state: CodeState) {
    status = state;
    errors++;
    await tick();
    field?.focus();
  }

  async function check(event: SubmitEvent) {
    event.preventDefault();
    if (busy || locked) return;
    if (!isComplete(text)) {
      await fail('incomplete');
      return;
    }
    busy = true;
    const code = normalizeCode(text);
    try {
      const candidate = await api.pairingCheck(code);
      onfound(code, candidate);
    } catch (err) {
      const result = codeError(err);
      lockedUntil = now + result.lockedFor * 1000;
      lockMinutes = Math.max(1, Math.ceil(result.lockedFor / 60));
      await fail(result.state);
    } finally {
      busy = false;
    }
  }
</script>

<form onsubmit={check} novalidate>
  <h2 id={headingId} tabindex="-1">{m.pair_step_code()}</h2>
  <p class="where">{m.pair_code_where()}</p>
  <div class="field">
    <label for="{id}-code">{m.pair_code_label()}</label>
    <input
      id="{id}-code"
      bind:this={field}
      bind:value={text}
      oninput={typed}
      readonly={locked}
      aria-disabled={locked ? 'true' : undefined}
      maxlength={MAX_INPUT}
      dir="ltr"
      autocomplete="off"
      autocapitalize="characters"
      spellcheck="false"
      enterkeyhint="go"
      aria-invalid={bad ? 'true' : undefined}
      aria-describedby="{id}-msg"
    />
    <!-- A new element per error, so a repeated error is announced again. -->
    {#key errors}
      {#if bad}
        <p id="{id}-msg" class="msg bad" role="alert"><Icon name="warning" size={16} /><span>{message}</span></p>
      {:else}
        <p id="{id}-msg" class="msg"><Icon name="info" size={16} /><span>{message}</span></p>
      {/if}
    {/key}
    <p class="hm-visually-hidden" role="status">{unlocked ? m.pair_code_unlocked() : ''}</p>
  </div>
  <div class="actions">
    <Button type="submit" variant="primary" size="lg" {busy} disabled={locked} aria-describedby="{id}-msg">{m.pair_code_submit()}</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
  }
  h2:focus {
    outline: none;
  }
  .where {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  label {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  input {
    max-inline-size: 16ch;
    min-block-size: 56px;
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    background: var(--hm-color-surface);
    color: var(--hm-color-text);
    font-family: var(--hm-font-mono);
    font-size: 24px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  input:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 0;
    border-color: var(--hm-color-accent);
  }
  input[aria-invalid='true'] {
    border-color: var(--hm-color-danger-fg);
  }
  input[readonly] {
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-disabled);
    border-color: var(--hm-color-border);
  }
  .msg {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .msg.bad {
    padding: 10px 12px;
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-danger-bg);
    color: var(--hm-color-danger-fg);
  }
  .actions {
    display: flex;
    gap: var(--hm-space-3);
  }
</style>
