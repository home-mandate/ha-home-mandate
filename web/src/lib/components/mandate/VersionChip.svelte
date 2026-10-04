<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Version and short hash of a mandate ("v4 · 3f9a1c2e"); a click copies the full digest,
  the same value the audit log shows. Where the clipboard is not available (an embedding
  frame may forbid it) the digest appears in a field, selected, to copy by hand.
-->
<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { m } from '../../i18n.ts';
  import { shortDigest } from '../../mandate/versions.ts';
  import Icon from '../Icon.svelte';

  const COPIED_MS = 2000;

  interface Props {
    version: number;
    digest: string;
    copy?: (text: string) => Promise<void>;
  }

  let { version, digest, copy = (text) => navigator.clipboard.writeText(text) }: Props = $props();

  const id = $props.id();
  let result = $state<'idle' | 'copied' | 'failed'>('idle');
  let field: HTMLInputElement | undefined = $state();
  let timer: ReturnType<typeof setTimeout> | undefined;

  const label = $derived(`${m.version_label({ version })} · ${shortDigest(digest)}`);
  const announced = $derived(result === 'copied' ? m.common_copied() : '');

  async function run() {
    clearTimeout(timer);
    try {
      await copy(digest);
      result = 'copied';
      timer = setTimeout(() => (result = 'idle'), COPIED_MS);
    } catch {
      result = 'failed';
      await tick();
      field?.focus();
      field?.select();
    }
  }

  onDestroy(() => clearTimeout(timer));
</script>

<span class="wrap">
  <button type="button" class="chip" title={m.hash_copy()} aria-label="{m.hash_label()} {label} · {m.hash_copy()}" onclick={run}>
    <span>{label}</span>
    <span class="icon"><Icon name={result === 'copied' ? 'check' : 'copy'} size={16} /></span>
  </button>
  <span class="hm-visually-hidden" role="status">{announced}</span>
  {#if result === 'failed'}
    <span class="manual">
      <input bind:this={field} id="{id}-digest" class="hm-mono" readonly value={digest} aria-label={m.hash_label()} aria-describedby="{id}-hint" />
      <span id="{id}-hint" role="alert">{m.common_copy_failed()}</span>
    </span>
  {/if}
</span>

<style>
  .wrap {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2);
    min-inline-size: 0;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: var(--hm-size-control-sm);
    padding-inline: var(--hm-space-2);
    border-radius: 6px;
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text);
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border);
    cursor: pointer;
  }
  .chip:hover {
    background: var(--hm-color-surface-pressed);
  }
  .icon {
    display: flex;
    color: var(--hm-color-text-subtle);
  }
  .manual {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    flex-basis: 100%;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  input {
    min-block-size: var(--hm-size-control);
    padding-inline: var(--hm-space-2);
    border-radius: var(--hm-radius-md);
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  @media (pointer: coarse), (max-width: 767px) {
    .chip {
      min-block-size: var(--hm-size-touch);
    }
  }
</style>
