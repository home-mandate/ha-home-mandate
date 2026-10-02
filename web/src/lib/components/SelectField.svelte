<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Native select, restyled (Components "Select"); area and device names stay untranslated.
  Options can follow in labelled groups (optgroup), e.g. areas and devices.
-->
<script lang="ts">
  import Icon from './Icon.svelte';

  interface Props {
    label: string;
    value: string;
    options: readonly { value: string; label: string }[];
    groups?: readonly { label: string; options: readonly { value: string; label: string }[] }[];
    help?: string;
    disabled?: boolean;
    onchange?: (value: string) => void;
  }

  let { label, value = $bindable(), options, groups = [], help, disabled = false, onchange }: Props = $props();

  const id = $props.id();
</script>

<div class="field">
  <label for={id}>{label}</label>
  <div class="control">
    <select
      {id}
      bind:value
      {disabled}
      aria-describedby={help ? `${id}-help` : undefined}
      onchange={() => onchange?.(value)}
    >
      {#each options as option (option.value)}<option value={option.value}>{option.label}</option>{/each}
      {#each groups as group (group.label)}
        <optgroup label={group.label}>
          {#each group.options as option (option.value)}<option value={option.value}>{option.label}</option>{/each}
        </optgroup>
      {/each}
    </select>
    <span class="chevron"><Icon name="chevronDown" /></span>
  </div>
  {#if help}<span id="{id}-help" class="help">{help}</span>{/if}
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
  .control {
    position: relative;
    display: flex;
  }
  select {
    appearance: none;
    inline-size: 100%;
    min-block-size: var(--hm-size-touch);
    padding-block: 0;
    padding-inline: var(--hm-space-3) var(--hm-space-10);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
  select:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 0;
  }
  select:disabled {
    color: var(--hm-color-text-disabled);
    background: var(--hm-color-surface-sunken);
    cursor: not-allowed;
  }
  .chevron {
    position: absolute;
    inset-inline-end: var(--hm-space-3);
    inset-block-start: var(--hm-space-3);
    display: flex;
    pointer-events: none;
    color: var(--hm-color-text-muted);
  }
  .help {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
</style>
