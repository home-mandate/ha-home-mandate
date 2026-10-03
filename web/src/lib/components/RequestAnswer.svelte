<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Answering an approval request in the UI (design README 6.9, decision F2). Decline and
  approve weigh the same. Decline acts at once; approve asks inline, non-modal, in one
  sentence what follows ("Claude Code will then unlock the front door") with the agent's
  name marked as its claim and the device isolated, and the focus on the group's first
  button. The confirmation counts only after a short pause, so a held Enter key or a
  double click on "Approve" cannot approve. Cancel returns the focus to "Approve".
-->
<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import type { ApprovalRequest } from '../api/types.ts';
  import { m } from '../i18n.ts';
  import { paramsText } from '../approvals/params.ts';
  import { actionLabel } from '../mandate/labels.ts';
  import { MARK, MARK2, around, pieces } from '../ui/sentence.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import AgentName from './AgentName.svelte';
  import Button from './Button.svelte';
  import DecisionBadge from './DecisionBadge.svelte';

  interface Props {
    request: ApprovalRequest;
    busy: boolean;
    /** ID of the card's heading: names which request the buttons answer. */
    describedBy?: string;
    onanswer: (approve: boolean) => void;
    now?: () => number;
  }

  let { request, busy, describedBy, onanswer, now = () => performance.now() }: Props = $props();

  /** The confirmation is armed this long after it appears. */
  const ARM_MS = 600;

  const id = $props.id();
  let confirming = $state(false);
  let shownAt = 0;
  /** Visible side of the arming delay: the button looks and reads as not ready yet (review a11y L9). */
  let armed = $state(true);
  let armTimer: ReturnType<typeof setTimeout> | undefined;
  onDestroy(() => clearTimeout(armTimer));
  let approveButton: HTMLButtonElement | undefined = $state();
  let confirmButton: HTMLButtonElement | undefined = $state();

  // Agent and device are components in the sentence, in the language's order.
  const sentence = $derived(pieces(m.request_approve_confirm({ agent: MARK, action: actionLabel(undefined, request.action), device: MARK2 })));
  // The confirmation repeats what is approved: the service data (security review S1).
  const values = $derived(paramsText(request.params));
  const valuesLine = around(m.request_confirm_values({ values: MARK }));

  async function ask() {
    confirming = true;
    shownAt = now();
    armed = false;
    clearTimeout(armTimer);
    armTimer = setTimeout(() => (armed = true), ARM_MS);
    await tick();
    confirmButton?.focus();
  }

  function confirm(event: MouseEvent) {
    // A held key repeats into the newly focused button; a double click lands on it.
    if (now() - shownAt < ARM_MS) {
      event.preventDefault();
      return;
    }
    onanswer(true);
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
    {#if request.critical}<DecisionBadge kind="critical" size="sm" />{/if}
    <p id="{id}-sentence">
      {#each sentence as piece, i (i)}{#if 'text' in piece}{piece.text}{:else if piece.slot === 1}<AgentName
            name={request.agent.display_name}
            client={request.agent.client_id}
          />{:else}<bdi>{cleanUntrusted(request.device_name)}</bdi>{/if}{/each}
    </p>
    {#if values}<p class="values">{valuesLine[0]}<bdi>{values}</bdi>{valuesLine[1]}</p>{/if}
    <div class="buttons">
      <Button variant="primary" {busy} disabled={!armed} bind:element={confirmButton} aria-describedby={describedBy} onclick={confirm}
        >{m.request_approve_confirm_btn()}</Button
      >
      <Button variant="secondary" disabled={busy} onclick={cancel}>{m.common_cancel()}</Button>
    </div>
  </div>
{:else}
  <div class="buttons">
    <Button variant="secondary" icon="deny" {busy} aria-describedby={describedBy} onclick={() => onanswer(false)}>{m.request_decline()}</Button>
    <Button variant="secondary" icon="allow" disabled={busy} aria-describedby={describedBy} bind:element={approveButton} onclick={ask}
      >{m.request_approve()}</Button
    >
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
