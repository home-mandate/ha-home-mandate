<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Allowing critical actions without approval (design README 6.5, decision U9): switching on
  does not switch it on. It opens an inline confirmation that names the agent, the actions
  and how many devices are affected; the safe choice comes first and gets the focus. Only
  "Allow without approval" sets it. Once on, the box turns red.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { m } from '../../i18n.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    on: boolean;
    /** Sentence that names agent, actions and devices. */
    consequence: string;
    onconfirm: () => void;
    onoff: () => void;
  }

  let { on, consequence, onconfirm, onoff }: Props = $props();

  const id = $props.id();
  let pending = $state(false);
  let toggle: HTMLButtonElement | undefined = $state();
  let keep: HTMLButtonElement | undefined = $state();

  async function switched() {
    if (on) {
      onoff();
      return;
    }
    pending = !pending;
    if (!pending) return;
    await tick();
    keep?.focus();
  }

  function close(confirmed: boolean) {
    pending = false;
    if (confirmed) onconfirm();
    toggle?.focus();
  }
</script>

<div class="box" class:on class:pending>
  <p class="lead">
    <span class="shield"><Icon name="critical" size={16} /></span>
    <span>{on ? m.critical_override_active() : m.demoted_hint()}</span>
  </p>
  <div class="row">
    <button
      bind:this={toggle}
      type="button"
      role="switch"
      aria-checked={on}
      aria-labelledby="{id}-label"
      aria-describedby="{id}-hint"
      onclick={switched}
    >
      <span class="knob"></span>
    </button>
    <span class="text">
      <span id="{id}-label" class="label">{m.critical_override_label()}</span>
      <span id="{id}-hint" class="hint">{on ? consequence : m.critical_override_off()}</span>
    </span>
  </div>
  {#if pending && !on}
    <div id="{id}-confirm" class="confirm" role="group" aria-labelledby="{id}-title" aria-describedby="{id}-body">
      <span id="{id}-title" class="title"><Icon name="warning" />{m.critical_confirm_title()}</span>
      <span id="{id}-body" class="body">{consequence}</span>
      <div class="buttons">
        <button bind:this={keep} type="button" class="keep" onclick={() => close(false)}>{m.critical_confirm_cancel()}</button>
        <button type="button" class="allow" onclick={() => close(true)}>{m.critical_confirm_action()}</button>
      </div>
    </div>
  {/if}
</div>

<style>
  .box {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: 14px;
    border-radius: 10px;
    border: var(--hm-border-width) solid var(--hm-color-ask-border);
    background: var(--hm-color-ask-bg);
  }
  .box.pending,
  .box.on {
    border: 2px solid var(--hm-color-danger-fg);
  }
  .box.on {
    background: var(--hm-color-danger-bg);
  }
  .lead {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-ask-fg);
  }
  .on .lead {
    color: var(--hm-color-danger-fg);
    font-weight: var(--hm-font-weight-semibold);
  }
  .shield {
    display: flex;
    padding-block-start: 2px;
    color: var(--hm-color-critical-fg);
  }
  .row {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-3);
    min-block-size: var(--hm-size-touch);
  }
  [role='switch'] {
    position: relative;
    flex-shrink: 0;
    inline-size: 44px;
    block-size: 26px;
    margin-block-start: 1px;
    padding: 0;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-pressed);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
  /* The whole 44 px row height is the hit area. */
  [role='switch']::before {
    content: '';
    position: absolute;
    inset: -9px 0;
  }
  .knob {
    position: absolute;
    inset-block-start: 2px;
    inset-inline-start: 2px;
    inline-size: 20px;
    block-size: 20px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface);
    box-shadow: var(--hm-shadow-sm);
  }
  [aria-checked='true'] {
    background: var(--hm-color-danger-solid);
    border-color: var(--hm-color-danger-solid);
  }
  [aria-checked='true'] .knob {
    inset-inline-start: 20px;
    background: var(--hm-color-on-danger);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .label {
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .hint {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  .on .hint {
    color: var(--hm-color-danger-fg);
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 14px;
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface);
    border: 2px solid var(--hm-color-danger-fg);
  }
  .title {
    display: flex;
    gap: var(--hm-space-2);
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
  }
  .body {
    font-size: 15px;
    color: var(--hm-color-text);
    text-wrap: pretty;
    overflow-wrap: anywhere;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
  .buttons button {
    min-block-size: var(--hm-size-touch);
    padding-inline: 14px;
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    cursor: pointer;
  }
  .keep {
    background: var(--hm-color-surface);
    color: var(--hm-color-text);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  .keep:hover {
    background: var(--hm-color-surface-hover);
  }
  .allow {
    border: var(--hm-border-width) solid transparent;
    background: var(--hm-color-danger-solid);
    color: var(--hm-color-on-danger);
  }
  .allow:hover {
    background: var(--hm-color-danger-solid-hover);
  }
  @media (forced-colors: active) {
    .knob {
      border: 1px solid CanvasText;
    }
    [aria-checked='true'] {
      forced-color-adjust: none;
      background: Highlight;
      border-color: Highlight;
    }
    .buttons button {
      border-color: ButtonText;
    }
  }
</style>
