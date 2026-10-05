<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Deleting one of the household's own templates: a destructive dialog with the focus on
  Cancel. Mandates made from the template stay as they are.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import { MARK, around } from '../../ui/sentence.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    open: boolean;
    /** Title of the template, cleaned. */
    title: string;
    busy: boolean;
    error: string;
    onclose: () => void;
    ondelete: () => void;
  }

  let { open, title, busy, error, onclose, ondelete }: Props = $props();

  const id = $props.id();
  let cancel: HTMLButtonElement | undefined = $state();

  const heading = $derived(around(m.template_delete_title({ template: MARK })));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" destructive initial={cancel ?? null} {onclose}>
  <div class="content">
    <h2 id="{id}-title">{heading[0]}<bdi>{title}</bdi>{heading[1]}</h2>
    <p id="{id}-body">{m.template_delete_body()}</p>
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" bind:element={cancel} disabled={busy} onclick={onclose}>{m.common_cancel()}</Button>
      <Button size="lg" variant="danger" {busy} onclick={ondelete}>{m.common_delete()}</Button>
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
  .error {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--hm-space-3);
  }
</style>
