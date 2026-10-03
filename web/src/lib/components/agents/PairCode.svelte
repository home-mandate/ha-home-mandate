<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing step 1 (design README 6.2): the code the agent shows. States idle, wrong,
  expired, locked (5 wrong codes; the field stays disabled until the server's lock time has
  passed) and failed (any other error). An incomplete code is not sent, so it costs no
  attempt. Errors are announced (role=alert) and tied to the field.
-->
<script lang="ts">
  import { untrack } from 'svelte';
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
    headingId: string;
    onfound: (code: string, candidate: PairingCandidate) => void;
  }

  let { api, now, initial = '', initialState = 'idle', headingId, onfound }: Props = $props();

  const id = $props.id();
  const MINUTE_MS = 60_000;
  // Longest input the field takes: 8 characters with a dash and some spaces.
  const MAX_INPUT = 12;

  let text = $state(untrack(() => initial));
  let status: CodeState = $state(untrack(() => initialState));
  let lockedUntil = $state(0);
  let busy = $state(false);
  let errors = $state(0);
  let field: HTMLInputElement | undefined = $state();

  const locked = $derived(status === 'locked' && now < lockedUntil);
  const shown = $derived<CodeState>(status === 'locked' && !locked ? 'idle' : status);
  const ready = $derived(isComplete(text) && !locked && !busy);
  const bad = $derived(shown !== 'idle');
  const message = $derived.by(() => {
    switch (shown) {
      case 'wrong':
        return m.pair_code_wrong();
      case 'expired':
        return m.pair_code_expired();
      case 'locked':
        return m.pair_code_locked({ minutes: Math.max(1, Math.ceil((lockedUntil - now) / MINUTE_MS)) });
      case 'failed':
        return m.pair_failed();
      default:
        return m.pair_code_help();
    }
  });

  function typed() {
    if (status !== 'locked') status = 'idle';
  }

  async function check(event: SubmitEvent) {
    event.preventDefault();
    if (!ready) return;
    busy = true;
    const code = normalizeCode(text);
    try {
      const candidate = await api.pairingCheck(code);
      onfound(code, candidate);
    } catch (err) {
      const result = codeError(err);
      status = result.state;
      errors++;
      lockedUntil = now + result.lockedFor * 1000;
      field?.focus();
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
      disabled={locked}
      maxlength={MAX_INPUT}
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
  </div>
  <div class="actions">
    <Button type="submit" variant="primary" size="lg" {busy} disabled={!ready && !busy}>{m.pair_code_submit()}</Button>
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
  input:disabled {
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
