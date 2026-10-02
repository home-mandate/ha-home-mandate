<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Decision of a rule as a radio group of three segment buttons (Components "Rule inputs").
  Arrow keys move and select (wrapping; left/right mirrored in right-to-left layouts), Tab
  leaves the group. Below it, one sentence on what the decision means.
-->
<script lang="ts">
  import type { Decision } from '../api/types.ts';
  import { m } from '../i18n.ts';
  import Icon from './Icon.svelte';

  interface Props {
    value: Decision;
    onchange?: (value: Decision) => void;
  }

  let { value = $bindable(), onchange }: Props = $props();

  const ORDER: readonly Decision[] = ['allow', 'ask', 'deny'];
  const VERB: Record<Decision, () => string> = {
    allow: () => m.decision_verb_allow(),
    ask: () => m.decision_verb_ask(),
    deny: () => m.decision_verb_deny(),
  };
  const DESC: Record<Decision, () => string> = {
    allow: () => m.decision_allow_desc(),
    ask: () => m.decision_ask_desc(),
    deny: () => m.decision_deny_desc(),
  };

  const id = $props.id();
  const buttons: HTMLButtonElement[] = $state([]);

  function pick(next: Decision) {
    if (next === value) return;
    value = next;
    onchange?.(next);
  }

  function keydown(event: KeyboardEvent) {
    const rtl = getComputedStyle(event.currentTarget as Element).direction === 'rtl';
    const forward = ['ArrowDown', rtl ? 'ArrowLeft' : 'ArrowRight'];
    const back = ['ArrowUp', rtl ? 'ArrowRight' : 'ArrowLeft'];
    const current = ORDER.indexOf(value);
    let index: number;
    if (forward.includes(event.key)) index = (current + 1) % ORDER.length;
    else if (back.includes(event.key)) index = (current - 1 + ORDER.length) % ORDER.length;
    else if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = ORDER.length - 1;
    else return;
    event.preventDefault();
    const next = ORDER[index] as Decision;
    pick(next);
    buttons[index]?.focus();
  }
</script>

<div class="segment">
  <span id="{id}-label" class="label">{m.decision_field_label()}</span>
  <div class="group" role="radiogroup" aria-labelledby="{id}-label" aria-describedby="{id}-desc">
    {#each ORDER as option, i (option)}
      <button
        bind:this={buttons[i]}
        type="button"
        role="radio"
        class={option}
        aria-checked={value === option}
        tabindex={value === option ? 0 : -1}
        onclick={() => pick(option)}
        onkeydown={keydown}
      >
        <Icon name={option} size={20} />
        <span>{VERB[option]()}</span>
      </button>
    {/each}
  </div>
  <span id="{id}-desc" class="desc">{DESC[value]()}</span>
</div>

<style>
  .segment {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .label {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .group {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--hm-space-1);
    padding: var(--hm-space-1);
    border-radius: 10px;
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border);
  }
  button {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    min-block-size: 52px;
    padding: 6px var(--hm-space-1);
    border-radius: 7px;
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
    text-align: center;
    overflow-wrap: anywhere;
    color: var(--hm-color-text-muted);
    background: transparent;
    border: 2px solid transparent;
    cursor: pointer;
  }
  button[aria-checked='true'] {
    font-weight: var(--hm-font-weight-semibold);
    box-shadow: var(--hm-shadow-sm);
  }
  .allow[aria-checked='true'] {
    color: var(--hm-color-allow-fg);
    background: var(--hm-color-allow-bg);
    border-color: var(--hm-color-allow-fg);
  }
  .ask[aria-checked='true'] {
    color: var(--hm-color-ask-fg);
    background: var(--hm-color-ask-bg);
    border-color: var(--hm-color-ask-fg);
  }
  .deny[aria-checked='true'] {
    color: var(--hm-color-deny-fg);
    background: var(--hm-color-deny-bg);
    border-color: var(--hm-color-deny-fg);
  }
  .desc {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  @media (forced-colors: active) {
    button[aria-checked='true'] {
      border-color: Highlight;
      outline: 2px solid Highlight;
      outline-offset: -4px;
    }
  }
</style>
