<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Reconnecting instead of admitting (issue #22): after an emergency stop, the agents of the
  same OAuth client that have no access any more are offered under the mandate step. The
  default stays admitting a new agent: nothing is chosen here until the person picks an
  agent, because several assistants can share one client. Reconnecting keeps the agent's
  entry, mandate and history; it is recorded as agent.reconnected. A pairing code's client
  ID is only the name the agent gave itself: then a warning with the address the request
  came from comes first.
-->
<script lang="ts">
  import type { ReconnectCandidate } from '../../api/types.ts';
  import { formatDate, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    candidates: readonly ReconnectCandidate[];
    /** The client ID was verified (metadata document); false for a pairing code's own name. */
    verified: boolean;
    /** Address the request came from. */
    from: string;
    ctx: FormatContext;
    busy: boolean;
    error: string;
    onreconnect: (clientId: string) => void;
  }

  let { candidates, verified, from, ctx, busy, error, onreconnect }: Props = $props();

  const id = $props.id();
  const ADDRESS_MAX = 45;
  let selected = $state('');

  function details(c: ReconnectCandidate): string {
    return m.pair_reconnect_details({
      mandate: c.mandate ? isolate(c.mandate.name) : m.pair_reconnect_no_mandate(),
      date: formatDate(new Date(c.admitted_at), ctx),
    });
  }
</script>

<section class="reconnect" aria-labelledby="{id}-title" aria-describedby="{id}-intro">
  <h3 id="{id}-title">{m.pair_reconnect_title()}</h3>
  {#if !verified}
    <p class="warn"><Icon name="warning" size={16} /><span>{m.pair_reconnect_unverified({ address: isolate(from, ADDRESS_MAX) })}</span></p>
  {/if}
  <p id="{id}-intro">{m.pair_reconnect_intro()}</p>
  <fieldset>
    <legend class="hm-visually-hidden">{m.pair_reconnect_choose()}</legend>
    {#each candidates as c (c.client_id)}
      <label class="option">
        <input type="radio" name="{id}-agent" value={c.client_id} bind:group={selected} disabled={busy} />
        <span class="text">
          <bdi class="name">{cleanUntrusted(c.display_name)}</bdi>
          <span class="about">{details(c)}</span>
        </span>
      </label>
    {/each}
  </fieldset>
  <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
  <div class="actions">
    <Button size="lg" {busy} disabled={selected === ''} onclick={() => onreconnect(selected)}>{m.pair_reconnect_button()}</Button>
  </div>
</section>

<style>
  .reconnect {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    margin: 0;
    padding: 0;
    border: 0;
    min-inline-size: 0;
  }
  .option {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-3);
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border);
    cursor: pointer;
  }
  .option:has(input:checked) {
    border-color: var(--hm-color-accent);
  }
  .option input {
    inline-size: 20px;
    block-size: 20px;
    margin: 2px 0 0;
    flex-shrink: 0;
    accent-color: var(--hm-color-accent);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-inline-size: 0;
  }
  .name {
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .about {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  .warn {
    display: flex;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-warning-bg);
    color: var(--hm-color-warning-fg);
    font-weight: var(--hm-font-weight-medium);
    overflow-wrap: anywhere;
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
    justify-content: flex-end;
  }
</style>
