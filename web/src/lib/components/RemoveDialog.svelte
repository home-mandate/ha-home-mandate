<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Removing revoked agents or mandates from the lists (issue #21): a destructive dialog with
  the focus on Cancel, like revoking. The body says what stays: the item stays revoked for
  good and the audit log keeps every entry. An optional choice (its revoked mandates too)
  starts as given and is sent with the confirmation.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { around } from '../ui/sentence.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import Button from './Button.svelte';
  import Dialog from './Dialog.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    open: boolean;
    /** Title with MARK where the name goes; without a name the title as it is. */
    title: string;
    name?: string;
    body: readonly string[];
    /** A choice offered with the removal, e.g. its revoked mandates too. */
    option?: string;
    optionChecked?: boolean;
    confirm: string;
    busy: boolean;
    error: string;
    onclose: () => void;
    onconfirm: (option: boolean) => void;
  }

  let { open, title, name, body, option, optionChecked = true, confirm, busy, error, onclose, onconfirm }: Props = $props();

  const id = $props.id();
  let cancel: HTMLButtonElement | undefined = $state();
  // The choice starts as given whenever the dialog opens.
  let checked = $state(true);
  $effect(() => {
    if (open) checked = optionChecked;
  });

  const parts = $derived(name === undefined ? [title, ''] : around(title));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" destructive initial={cancel ?? null} {onclose}>
  <div class="content">
    <h2 id="{id}-title">{parts[0]}{#if name !== undefined}<bdi>{cleanUntrusted(name)}</bdi>{/if}{parts[1]}</h2>
    <div id="{id}-body" class="body">
      {#each body as paragraph, i (i)}<p>{paragraph}</p>{/each}
    </div>
    {#if option}
      <label class="check">
        <input type="checkbox" bind:checked disabled={busy} />
        <span>{option}</span>
      </label>
    {/if}
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" bind:element={cancel} disabled={busy} onclick={onclose}>{m.common_cancel()}</Button>
      <Button size="lg" variant="danger" {busy} onclick={() => onconfirm(option !== undefined && checked)}>{confirm}</Button>
    </div>
  </div>
</Dialog>

<style>
  .content {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  p {
    margin: 0;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .body p + p {
    margin-block-start: var(--hm-space-2);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    font-weight: var(--hm-font-weight-medium);
  }
  .check input {
    inline-size: 20px;
    block-size: 20px;
    margin: 0;
    flex-shrink: 0;
    accent-color: var(--hm-color-accent);
  }
  .error {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .error:empty {
    display: none;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--hm-space-3);
  }
</style>
