<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Answering an approval request in the UI (design README 6.9, decision F2). Decline and
  approve weigh the same. Decline acts at once; approve asks inline, non-modal, in one
  sentence what follows ("Claude Code will then unlock the front door"), with the focus on
  the group's first button. Cancel returns the focus to "Approve".
-->
<script lang="ts">
  import { tick } from 'svelte';
  import type { ApprovalRequest } from '../api/types.ts';
  import { m } from '../i18n.ts';
  import { actionLabel } from '../mandate/labels.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import Button from './Button.svelte';

  interface Props {
    request: ApprovalRequest;
    busy: boolean;
    onanswer: (approve: boolean) => void;
  }

  let { request, busy, onanswer }: Props = $props();

  const id = $props.id();
  let confirming = $state(false);
  let approveButton: HTMLButtonElement | undefined = $state();
  let confirmButton: HTMLButtonElement | undefined = $state();

  const sentence = $derived(
    m.request_approve_confirm({
      agent: cleanUntrusted(request.agent.display_name),
      action: actionLabel(undefined, request.action),
      device: cleanUntrusted(request.device_name),
    }),
  );

  async function ask() {
    confirming = true;
    await tick();
    confirmButton?.focus();
  }

  async function cancel() {
    confirming = false;
    await tick();
    approveButton?.focus();
  }
</script>

{#if confirming}
  <div class="confirm" role="group" aria-labelledby="{id}-label" aria-describedby="{id}-sentence">
    <span id="{id}-label" class="hm-visually-hidden">{m.request_confirm_label()}</span>
    <p id="{id}-sentence"><bdi>{sentence}</bdi></p>
    <div class="buttons">
      <Button variant="primary" {busy} bind:element={confirmButton} onclick={() => onanswer(true)}>{m.request_approve_confirm_btn()}</Button>
      <Button variant="secondary" disabled={busy} onclick={cancel}>{m.common_cancel()}</Button>
    </div>
  </div>
{:else}
  <div class="buttons">
    <Button variant="secondary" icon="deny" {busy} onclick={() => onanswer(false)}>{m.request_decline()}</Button>
    <Button variant="secondary" icon="allow" disabled={busy} bind:element={approveButton} onclick={ask}>{m.request_approve()}</Button>
  </div>
{/if}

<style>
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-ask-bg);
    border: var(--hm-border-width) solid var(--hm-color-ask-border);
  }
  p {
    margin: 0;
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
</style>
