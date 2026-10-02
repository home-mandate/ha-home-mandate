<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Approval settings: the timeout as number and unit, and who gets the push notification.
  Used for the mandate's default and for a rule's own settings. Errors show once the field
  was left or a save was tried, never while typing.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import type { Approval } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { timeoutInput, timeoutIso, type TimeoutUnit } from '../../mandate/timeout.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    approval: Approval;
    /** People who can approve: Home Assistant user id and name. */
    people: readonly { id: string; name: string }[];
    timeoutError?: string;
    approversError?: string;
    disabled?: boolean;
    onchange: (approval: Approval) => void;
    /** A field was left or a person was switched. */
    ontouch?: (part: 'timeout' | 'approvers') => void;
  }

  let { approval, people, timeoutError = '', approversError = '', disabled = false, onchange, ontouch }: Props = $props();

  const id = $props.id();
  const MAX_INITIALS = 2;

  // What is typed stays as typed; it only follows the draft when that changed elsewhere.
  // A number input binds a number (or null while it is empty or not a number).
  let value: string | number | null = $state('');
  let unit: TimeoutUnit = $state('m');
  const typed = () => timeoutIso({ value: String(value ?? ''), unit });
  $effect(() => {
    const stored = approval.timeout;
    untrack(() => {
      if (typed() === stored) return;
      const input = timeoutInput(stored);
      value = input.value;
      if (input.value !== '') unit = input.unit;
    });
  });

  function emitTimeout() {
    onchange({ ...approval, timeout: typed() });
  }

  function toggle(person: string) {
    const approvers = approval.approvers.includes(person) ? approval.approvers.filter((a) => a !== person) : [...approval.approvers, person];
    onchange({ ...approval, approvers });
    ontouch?.('approvers');
  }

  function initials(name: string): string {
    return name
      .split(/\s+/u)
      .slice(0, MAX_INITIALS)
      .map((word) => [...word][0]?.toLocaleUpperCase() ?? '')
      .join('');
  }
</script>

<div class="fields">
  <div class="field">
    <span id="{id}-timeout" class="label">{m.timeout_label()}</span>
    <div class="timeout" role="group" aria-labelledby="{id}-timeout">
      <input
        type="number"
        inputmode="numeric"
        min="1"
        bind:value
        {disabled}
        aria-labelledby="{id}-timeout"
        aria-describedby="{id}-timeout-help"
        aria-invalid={timeoutError ? 'true' : undefined}
        oninput={emitTimeout}
        onblur={() => ontouch?.('timeout')}
      />
      <span id="{id}-unit" class="hm-visually-hidden">{m.timeout_unit_label()}</span>
      <select bind:value={unit} {disabled} aria-labelledby="{id}-timeout {id}-unit" onchange={emitTimeout}>
        <option value="s">{m.unit_seconds()}</option>
        <option value="m">{m.unit_minutes()}</option>
      </select>
    </div>
    <span id="{id}-timeout-help" class="help" class:error={timeoutError}>
      {#if timeoutError}<Icon name="warning" size={16} />{timeoutError}{:else}{m.timeout_help()}{/if}
    </span>
  </div>

  <fieldset aria-describedby="{id}-approvers-help">
    <legend>{m.editor_approvers()}</legend>
    <div class="people">
      {#each people as person (person.id)}
        {@const on = approval.approvers.includes(person.id)}
        <button type="button" class="person" aria-pressed={on} aria-disabled={disabled ? 'true' : undefined} onclick={() => !disabled && toggle(person.id)}>
          <span class="avatar" aria-hidden="true">{initials(cleanUntrusted(person.name))}</span>
          <bdi>{cleanUntrusted(person.name)}</bdi>
          {#if on}<Icon name="check" size={16} />{/if}
        </button>
      {/each}
    </div>
    <span id="{id}-approvers-help" class="help" class:error={approversError}>
      {#if approversError}<Icon name="warning" size={16} />{approversError}{:else}{m.editor_approvers_help()}{/if}
    </span>
  </fieldset>
</div>

<style>
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: var(--hm-space-4);
  }
  .field,
  fieldset {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-inline-size: 0;
    margin: 0;
    padding: 0;
    border: none;
  }
  .label,
  legend {
    padding: 0;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  legend {
    padding-block-end: 6px;
  }
  .timeout {
    display: flex;
    gap: var(--hm-space-2);
  }
  input,
  select {
    min-block-size: var(--hm-size-touch);
    padding-block: 0;
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  input {
    inline-size: 88px;
    box-sizing: border-box;
  }
  select {
    flex: 1;
    min-inline-size: 0;
  }
  input[aria-invalid='true'] {
    border-color: var(--hm-color-danger-fg);
    outline: 1px solid var(--hm-color-danger-fg);
    outline-offset: 0;
  }
  input:disabled,
  select:disabled {
    color: var(--hm-color-text-disabled);
    background: var(--hm-color-surface-sunken);
    border-color: var(--hm-color-border);
  }
  .people {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .person {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    padding-inline: 6px var(--hm-space-3);
    border-radius: var(--hm-radius-pill);
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
  .person[aria-pressed='true'] {
    font-weight: var(--hm-font-weight-semibold);
    background: var(--hm-color-accent-subtle);
    border: 2px solid var(--hm-color-accent);
  }
  .person[aria-disabled='true'] {
    color: var(--hm-color-text-disabled);
    cursor: not-allowed;
  }
  .avatar {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 30px;
    block-size: 30px;
    border-radius: var(--hm-radius-pill);
    font-size: 12px;
    font-weight: var(--hm-font-weight-semibold);
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-muted);
  }
  .person[aria-pressed='true'] .avatar {
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .help {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .error {
    color: var(--hm-color-danger-fg);
  }
  @media (forced-colors: active) {
    .person[aria-pressed='true'] {
      border-color: Highlight;
    }
  }
</style>
