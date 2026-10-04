<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Read-only value (URL, code) with a copy button; "Copied" for 2 s after copying. The result
  is announced from a status region next to the button. Without clipboard access (likely in
  the Ingress iframe) the text is selected and the user told to copy it by hand.
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import { m } from '../i18n.ts';
  import Icon from './Icon.svelte';

  const COPIED_MS = 2000;

  interface Props {
    label: string;
    value: string;
    help?: string;
    copy?: (text: string) => Promise<void>;
  }

  let { label, value, help, copy = (text) => navigator.clipboard.writeText(text) }: Props = $props();

  const id = $props.id();
  let input: HTMLInputElement | undefined = $state();
  let result = $state<'idle' | 'copied' | 'failed'>('idle');
  let timer: ReturnType<typeof setTimeout> | undefined;
  const copied = $derived(result === 'copied');
  const failed = $derived(result === 'failed');

  async function run() {
    clearTimeout(timer);
    try {
      await copy(value);
      result = 'copied';
      timer = setTimeout(() => (result = 'idle'), COPIED_MS);
    } catch {
      result = 'failed';
      input?.focus();
      input?.select();
    }
  }

  onDestroy(() => clearTimeout(timer));
</script>

<div class="field">
  <label id="{id}-label" for={id}>{label}</label>
  <div class="control">
    <input bind:this={input} {id} type="text" readonly dir="ltr" {value} aria-describedby={help ? `${id}-help` : undefined} />
    <button type="button" class:copied aria-describedby="{id}-label" onclick={run}>
      <Icon name={copied ? 'check' : 'copy'} size={16} />
      <span>{copied ? m.common_copied() : m.common_copy()}</span>
    </button>
  </div>
  {#if help}<span id="{id}-help" class="help">{help}</span>{/if}
  <span class="result" class:hm-visually-hidden={!failed} role="status">
    {#if copied}{m.common_copied()}{:else if failed}{m.common_copy_failed()}{/if}
  </span>
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
    display: flex;
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    background: var(--hm-color-surface-sunken);
    overflow: hidden;
  }
  input {
    flex: 1;
    min-inline-size: 0;
    padding-inline: var(--hm-space-3);
    border: none;
    background: transparent;
    color: var(--hm-color-text);
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-sm);
  }
  input:focus-visible,
  button:focus-visible {
    outline-offset: -2px;
  }
  button {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: var(--hm-size-touch);
    padding-inline: 14px;
    border: none;
    border-inline-start: var(--hm-border-width) solid var(--hm-color-border);
    background: var(--hm-color-surface);
    color: var(--hm-color-text);
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    cursor: pointer;
  }
  .copied {
    color: var(--hm-color-positive-fg);
  }
  .help,
  .result {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .result:not(.hm-visually-hidden) {
    color: var(--hm-color-warning-fg);
  }
</style>
