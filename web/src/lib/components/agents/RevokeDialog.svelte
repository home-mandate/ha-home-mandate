<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Revoking an agent (design README 6.2 and 7; decisions F1, G2): a destructive dialog with
  the focus on Cancel. Revoking is final and declines pending approvals. No typing of the
  name: agents' names can be long or right-to-left, and the safe default button is enough.
  Revoking can also remove the agent and its mandates from the lists in the same step
  (leftovers after an emergency stop, #21); that choice starts off.
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
    /** remove: also remove the agent and its mandates from the lists. */
    onrevoke: (remove: boolean) => void;
  }

  let { open, name, busy, error, onclose, onrevoke }: Props = $props();

  const id = $props.id();
  let cancel: HTMLButtonElement | undefined = $state();
  let remove = $state(false);
  $effect(() => {
    if (open) remove = false;
  });

  const title = $derived(around(m.revoke_title({ agent: MARK })));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" destructive initial={cancel ?? null} {onclose}>
  <div class="content">
    <h2 id="{id}-title">{title[0]}<bdi>{cleanUntrusted(name)}</bdi>{title[1]}</h2>
    <div id="{id}-body" class="body">
      <p>{m.revoke_body()}</p>
      <p>{m.agent_detail_end_desc()}</p>
    </div>
    <label class="check">
      <input type="checkbox" bind:checked={remove} disabled={busy} aria-describedby="{id}-remove" />
      <span>{m.revoke_also_remove()}</span>
    </label>
    <p id="{id}-remove" class="hint">{m.revoke_also_remove_hint()}</p>
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" bind:element={cancel} disabled={busy} onclick={onclose}>{m.common_cancel()}</Button>
      <Button size="lg" variant="danger" {busy} onclick={() => onrevoke(remove)}>{remove ? m.revoke_remove_button() : m.revoke_button()}</Button>
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
  .hint {
    margin-block-start: calc(-1 * var(--hm-space-3));
    font-size: var(--hm-font-size-sm);
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
