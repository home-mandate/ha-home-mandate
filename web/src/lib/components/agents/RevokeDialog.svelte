<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Revoking an agent (design README 6.2 and 7; decisions F1, G2): a destructive dialog with
  the focus on Cancel. Revoking is final and declines pending approvals. No typing of the
  name: agents' names can be long or right-to-left, and the safe default button is enough.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import { MARK, around } from '../../ui/sentence.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    open: boolean;
    name: string;
    busy: boolean;
    error: string;
    onclose: () => void;
    onrevoke: () => void;
  }

  let { open, name, busy, error, onclose, onrevoke }: Props = $props();

  const id = $props.id();
  let cancel: HTMLButtonElement | undefined = $state();

  const title = $derived(around(m.revoke_title({ agent: MARK })));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" destructive initial={cancel ?? null} {onclose}>
  <div class="content">
    <h2 id="{id}-title">{title[0]}<bdi>{cleanUntrusted(name)}</bdi>{title[1]}</h2>
    <div id="{id}-body" class="body">
      <p>{m.revoke_body()}</p>
      <p>{m.agent_detail_end_desc()}</p>
    </div>
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" bind:element={cancel} disabled={busy} onclick={onclose}>{m.common_cancel()}</Button>
      <Button size="lg" variant="danger" {busy} onclick={onrevoke}>{m.revoke_button()}</Button>
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
