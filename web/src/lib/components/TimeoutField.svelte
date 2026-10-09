<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The approval timeout as number and unit (seconds or minutes), for a mandate, a rule or
  the default in the settings. What is typed stays as typed; it only follows the value
  when that changed elsewhere. Errors come from the caller. With the installation's upper
  limit (max), the field offers no more and shows a longer timeout as capped: the server
  waits at most that long.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { m } from '../i18n.ts';
  import { cappedBy, inputMax, timeoutInput, timeoutIso, timeoutText, type TimeoutUnit } from '../mandate/timeout.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import Icon from './Icon.svelte';

  interface Props {
    /** ISO 8601 duration; "" while the input is no whole number. */
    timeout: string;
    error?: string;
    help?: string;
    /** The installation's upper limit in seconds; null while unknown. */
    max?: number | null;
    disabled?: boolean;
    onchange: (timeout: string) => void;
    /** The field was left. */
    ontouch?: () => void;
  }

  let { timeout, error = '', help, max = null, disabled = false, onchange, ontouch }: Props = $props();

  const id = $props.id();

  // A number input binds a number (or null while it is empty or not a number).
  let value: string | number | null = $state('');
  let unit: TimeoutUnit = $state('m');
  const typed = () => timeoutIso({ value: String(value ?? ''), unit });
  $effect(() => {
    const stored = timeout;
    untrack(() => {
      if (typed() === stored) return;
      const input = timeoutInput(stored);
      value = input.value;
      if (input.value !== '') unit = input.unit;
    });
  });

  const emit = () => onchange(typed());

  const maxText = $derived(max === null ? '' : timeoutText(`PT${max}S`, getLocale()));
  const capped = $derived(cappedBy(timeout, max) !== null);
  const note = $derived(capped ? m.timeout_capped({ max: maxText }) : (help ?? (max === null ? m.timeout_help() : m.timeout_help_max({ max: maxText }))));
</script>

<div class="field">
  <span id="{id}-timeout" class="label">{m.timeout_label()}</span>
  <div class="timeout" role="group" aria-labelledby="{id}-timeout">
    <input
      type="number"
      inputmode="numeric"
      min="1"
      max={inputMax(max, unit)}
      bind:value
      {disabled}
      aria-labelledby="{id}-timeout"
      aria-describedby="{id}-timeout-help"
      aria-invalid={error ? 'true' : undefined}
      oninput={emit}
      onblur={() => ontouch?.()}
    />
    <span id="{id}-unit" class="hm-visually-hidden">{m.timeout_unit_label()}</span>
    <select bind:value={unit} {disabled} aria-labelledby="{id}-timeout {id}-unit" onchange={emit}>
      <option value="s">{m.unit_seconds()}</option>
      <option value="m">{m.unit_minutes()}</option>
    </select>
  </div>
  <span id="{id}-timeout-help" class="help" class:error class:capped={capped && !error}>
    {#if error}<Icon name="warning" size={16} />{error}{:else if capped}<Icon name="info" size={16} />{note}{:else}{note}{/if}
  </span>
</div>

<style>
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-inline-size: 0;
  }
  .label {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
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
    /* A shadow, not an outline: the outline stays free for the focus ring. */
    box-shadow: 0 0 0 1px var(--hm-color-danger-fg);
  }
  input:disabled,
  select:disabled {
    color: var(--hm-color-text-disabled);
    background: var(--hm-color-surface-sunken);
    border-color: var(--hm-color-border);
  }
  .help {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .help.error {
    color: var(--hm-color-danger-fg);
  }
  .help.capped {
    color: var(--hm-color-text);
  }
</style>
