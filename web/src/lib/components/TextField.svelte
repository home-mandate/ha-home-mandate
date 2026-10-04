<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Labelled text input with help or error text wired through aria-describedby. 44 px high,
  16 px text (no iOS zoom). Errors set aria-invalid and show the warning icon.
-->
<script lang="ts">
  import type { HTMLInputAttributes } from 'svelte/elements';
  import Icon from './Icon.svelte';

  interface Props extends Omit<HTMLInputAttributes, 'value'> {
    label: string;
    value: string;
    help?: string;
    error?: string;
    mono?: boolean;
    element?: HTMLInputElement;
  }

  let {
    label,
    value = $bindable(),
    help,
    error,
    mono = false,
    disabled,
    element = $bindable(),
    'aria-describedby': describedby,
    ...rest
  }: Props = $props();

  const id = $props.id();
  const description = $derived([error || help ? `${id}-help` : '', describedby ?? ''].filter(Boolean).join(' ') || undefined);
</script>

<div class="field" class:disabled>
  <label for={id}>{label}</label>
  <input
    bind:this={element}
    bind:value
    {...rest}
    {id}
    {disabled}
    class:mono
    aria-invalid={error ? 'true' : undefined}
    aria-describedby={description}
  />
  {#if error}
    <span id="{id}-help" class="help error"><Icon name="warning" size={16} />{error}</span>
  {:else if help}
    <span id="{id}-help" class="help">{help}</span>
  {/if}
</div>

<style>
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
  input:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 0;
    border-color: var(--hm-color-accent);
  }
  input[aria-invalid='true'] {
    border-color: var(--hm-color-danger-fg);
    /* A shadow, not an outline: the outline stays free for the focus ring. */
    box-shadow: 0 0 0 1px var(--hm-color-danger-fg);
  }
  .mono {
    font-family: var(--hm-font-mono);
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
  .disabled label,
  .disabled .help {
    color: var(--hm-color-text-disabled);
  }
  input:disabled {
    color: var(--hm-color-text-disabled);
    background: var(--hm-color-surface-sunken);
    border-color: var(--hm-color-border);
  }
</style>
