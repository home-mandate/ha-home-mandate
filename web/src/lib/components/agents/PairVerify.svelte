<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing step 2 (design README 6.2): is this the agent the person started? The client
  identifier comes first and in mono (it is what the agent shows); the name is the agent's
  claim. The code and the address the request came from help to tell agents apart (G4).
  "This isn't my agent" declines the pairing, so the code is used up.
-->
<script lang="ts">
  import type { PairingCandidate } from '../../api/types.ts';
  import { displayCode } from '../../agents/pairing.ts';
  import { formatDateTime, formatRelative, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { isolate } from '../../untrusted.ts';
  import AgentName from '../AgentName.svelte';
  import Button from '../Button.svelte';
  import ClientIdentity from '../ClientIdentity.svelte';

  interface Props {
    code: string;
    candidate: PairingCandidate;
    ctx: FormatContext;
    /** Server clock in ms. */
    serverNow: number;
    headingId: string;
    busy: boolean;
    onnotmine: () => void;
    oncontinue: () => void;
  }

  let { code, candidate, ctx, serverNow, headingId, busy, onnotmine, oncontinue }: Props = $props();

  const requestedAt = $derived(new Date(candidate.requested_at));
  const requested = $derived(
    m.pair_verify_requested({ relative: formatRelative(requestedAt, new Date(serverNow), ctx), address: isolate(candidate.requested_from) }),
  );
</script>

<section aria-labelledby={headingId}>
  <h2 id={headingId} tabindex="-1">{m.pair_verify_title()}</h2>
  <p class="lead">{m.pair_verify_body()}</p>
  <div class="card">
    <span class="client"><ClientIdentity client={candidate.client} /></span>
    <span class="name"><AgentName name={candidate.claimed_name} /></span>
    <span class="fact">{m.pair_verify_code_match({ code: displayCode(code) })}</span>
    <span class="fact" title={formatDateTime(requestedAt, ctx)}>{requested}</span>
  </div>
  <div class="actions">
    <Button size="lg" variant="text" disabled={busy} onclick={onnotmine}>{m.pair_not_mine()}</Button>
    <Button size="lg" variant="primary" disabled={busy} onclick={oncontinue}>{m.common_continue()}</Button>
  </div>
</section>

<style>
  section {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
  }
  h2:focus {
    outline: none;
  }
  .lead {
    margin: 0;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    overflow-wrap: anywhere;
  }
  .client {
    font-size: var(--hm-font-size-lg);
  }
  .client :global(.domain),
  .client :global(.claimed) {
    font-size: var(--hm-font-size-lg);
  }
  .name {
    font-size: var(--hm-font-size-md);
  }
  .fact {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--hm-space-3);
  }
</style>
