<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Emergency stop sheet (design README section 5, changed by decision H1 of 03.10.2026):
  what happens, Cancel and an explicit confirmation instead of holding for 2 s, so every
  way of input reaches it. Focus starts on Cancel; the confirmation is armed after a
  moment, so a double click or a held Enter cannot trigger it. A click beside the sheet,
  Escape and Cancel cancel; the window losing focus does not.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { onDestroy } from 'svelte';
  import Button from './Button.svelte';
  import Dialog from './Dialog.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    open: boolean;
    onclose: () => void;
    onfire: () => void;
    /** The request is on its way. */
    busy?: boolean;
    /** Shown in the sheet as an alert: the app behind a modal sheet is inert, toasts would not be heard. */
    error?: string;
  }

  let { open, onclose, onfire, busy = false, error }: Props = $props();

  /** The confirmation is armed this long after the sheet opens (like "Yes, approve"). */
  const ARM_MS = 600;

  const id = $props.id();
  let cancel: HTMLButtonElement | undefined = $state();
  let armed = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  $effect(() => {
    if (!open) return;
    armed = false;
    timer = setTimeout(() => (armed = true), ARM_MS);
    return () => clearTimeout(timer);
  });
  onDestroy(() => clearTimeout(timer));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" destructive backdropCloses initial={cancel ?? null} {onclose}>
  <div class="sheet">
    <div class="head">
      <span class="icon"><Icon name="power" size={32} /></span>
      <div class="text">
        <h2 id="{id}-title">{m.estop_sheet_title()}</h2>
        <p id="{id}-body">{m.estop_sheet_body()}</p>
      </div>
      <button type="button" class="close" aria-label={m.common_close()} onclick={onclose}><Icon name="close" /></button>
    </div>
    <p class="after">{m.estop_sheet_after()}</p>
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" bind:element={cancel} onclick={onclose}>{m.common_cancel()}</Button>
      <Button size="lg" variant="danger" icon="power" disabled={!armed} {busy} onclick={onfire}>{m.estop_confirm()}</Button>
    </div>
  </div>
</Dialog>

<style>
  .sheet {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  .head {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-4);
  }
  .icon {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    inline-size: 48px;
    block-size: 48px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-danger-bg);
    color: var(--hm-color-danger-fg);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1;
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    line-height: 1.3;
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
  }
  .after {
    font-size: 15px;
    color: var(--hm-color-text-muted);
  }
  .error {
    display: flex;
    gap: var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .error:empty {
    display: none;
  }
  .close {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: var(--hm-size-touch);
    block-size: var(--hm-size-touch);
    margin-block-start: -10px;
    margin-inline-end: -10px;
    border: none;
    border-radius: var(--hm-radius-md);
    background: transparent;
    color: var(--hm-color-text-muted);
    cursor: pointer;
  }
  .close:hover {
    background: var(--hm-color-surface-hover);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--hm-space-3);
  }
  @media (max-width: 767px) {
    /* Bottom sheet: Cancel on top, the confirmation below, both full width. */
    .actions {
      flex-direction: column;
      align-items: stretch;
    }
  }
</style>
