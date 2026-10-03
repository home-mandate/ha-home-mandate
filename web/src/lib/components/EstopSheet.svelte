<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Emergency stop sheet (design README section 5): what happens, the 2 s hold button and
  Cancel. Destructive: focus starts on the hold button, a backdrop click does not close it.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import Dialog from './Dialog.svelte';
  import HoldButton from './HoldButton.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    open: boolean;
    onclose: () => void;
    onfire: () => void;
    /** Shown in the sheet as an alert: the app behind a modal sheet is inert, toasts would not be heard. */
    error?: string;
  }

  let { open, onclose, onfire, error }: Props = $props();

  const id = $props.id();
  let hold: HTMLButtonElement | undefined = $state();
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" destructive initial={hold ?? null} {onclose}>
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
    <HoldButton bind:element={hold} label={m.estop_hold()} done={m.estop_hold_firing()} {onfire} />
    <button type="button" class="cancel" onclick={onclose}>{m.common_cancel()}</button>
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
  .cancel {
    min-block-size: var(--hm-size-touch);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
</style>
