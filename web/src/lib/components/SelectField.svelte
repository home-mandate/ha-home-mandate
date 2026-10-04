<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Native select, restyled (Components "Select"); area and device names stay untranslated.
  Options can follow in labelled groups (optgroup), e.g. areas and devices.
  Some systems (Windows) report a change for every arrow key on a closed select. Browsing
  with keys is therefore taken only 400 ms after the last key; Enter, leaving the field
  and a choice from the open list (pointer, touch) are taken at once (decision L4).
-->
<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import Icon from './Icon.svelte';

  interface Props {
    label: string;
    value: string;
    /** lang: the option's own language (a language name in that language). */
    options: readonly { value: string; label: string; lang?: string }[];
    groups?: readonly { label: string; options: readonly { value: string; label: string }[] }[];
    help?: string;
    disabled?: boolean;
    onchange?: (value: string) => void;
  }

  let { label, value = $bindable(), options, groups = [], help, disabled = false, onchange }: Props = $props();

  const id = $props.id();
  /** Pause after the last browsing key before its value counts. */
  const SETTLE_MS = 400;
  const BROWSE_KEYS = new Set(['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown']);

  /** The option on screen; value and onchange follow when it is taken. */
  let shown = $state(untrack(() => value));
  let browsing = false;
  let timer: ReturnType<typeof setTimeout> | undefined;

  // A new value from outside replaces what is shown, unless a browse is still pending.
  $effect(() => {
    const outside = value;
    untrack(() => {
      if (timer === undefined) shown = outside;
    });
  });

  function take() {
    clearTimeout(timer);
    timer = undefined;
    if (shown === value) return;
    value = shown;
    onchange?.(shown);
  }

  function changed() {
    if (!browsing) {
      take();
      return;
    }
    browsing = false;
    clearTimeout(timer);
    timer = setTimeout(take, SETTLE_MS);
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Enter') take();
    // Letters jump to an option as well; Space opens the list.
    else browsing = BROWSE_KEYS.has(event.key) || (event.key.length === 1 && event.key !== ' ');
  }

  onDestroy(() => {
    if (timer !== undefined) take();
  });
</script>

<div class="field">
  <label for={id}>{label}</label>
  <div class="control">
    <select
      {id}
      bind:value={shown}
      {disabled}
      aria-describedby={help ? `${id}-help` : undefined}
      onkeydown={keydown}
      onpointerdown={() => (browsing = false)}
      onchange={changed}
      onblur={() => timer !== undefined && take()}
    >
      {#each options as option (option.value)}<option value={option.value} lang={option.lang}>{option.label}</option>{/each}
      {#each groups.filter((g) => g.options.length > 0) as group (group.label)}
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
