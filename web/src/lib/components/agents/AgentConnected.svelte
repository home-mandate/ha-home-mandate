<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The end of both ways to connect an agent (#35): pairing by code and browser sign-in.
  Names the agent and its mandate, links to both, and lists the next steps: check the
  mandate, make sure approval requests reach someone (from the server's view of the
  mandate's approvers, never computed here), and try a first request. When the server
  cannot tell whether requests reach anyone, it says so and never shows them as reachable.
-->
<script lang="ts">
  import type { Agent, TemplateApprovers } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { href } from '../../router.ts';
  import { MARK, around } from '../../ui/sentence.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Banner from '../Banner.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    agent: Agent;
    /** The mandate's name; the agent's own when empty. */
    mandateName?: string;
    /** The agent was reconnected to its existing entry, not admitted anew. */
    reconnected?: boolean;
    /** ID of the heading, which the page focuses. */
    headingId: string;
    approvers: (mandateId: string) => Promise<TemplateApprovers>;
  }

  let { agent, mandateName = '', reconnected = false, headingId, approvers }: Props = $props();

  const id = $props.id();
  const title = $derived(around(m.pair_done_title({ agent: MARK })));
  const mandate = $derived(mandateName || agent.mandate?.name || '');

  let coverage = $state.raw<TemplateApprovers | null>(null);
  let failed = $state(false);

  $effect(() => {
    const mandateId = agent.mandate?.id;
    let current = true;
    coverage = null;
    failed = false;
    if (!mandateId) return;
    approvers(mandateId).then(
      (c) => {
        if (current) coverage = c;
      },
      () => {
        if (current) failed = true;
      },
    );
    return () => {
      current = false;
    };
  });

  const unknown = $derived(failed || coverage?.normal === 'unknown' || coverage?.critical === 'unknown');
  const nobody = $derived(!unknown && (coverage?.normal === 'nobody' || coverage?.critical === 'nobody'));
  const notNeeded = $derived(coverage?.normal === 'not_needed' && coverage.critical === 'not_needed');
</script>

<section class="done" aria-labelledby={headingId}>
  <span class="icon"><Icon name="check" size={32} /></span>
  <h2 id={headingId} tabindex="-1">{title[0]}<bdi>{cleanUntrusted(agent.display_name)}</bdi>{title[1]}</h2>
  <p>{reconnected ? m.pair_reconnected_body({ mandate: isolate(mandate) }) : m.pair_done_body({ mandate: isolate(mandate) })}</p>
  <div class="links">
    <a class="primary" href={href({ name: 'agent', id: agent.client_id })}>{m.pair_done_open()}</a>
    {#if agent.mandate}<a href={href({ name: 'mandate', id: agent.mandate.id })}>{m.pair_done_edit()}</a>{/if}
  </div>

  <h3 id="{id}-next">{m.connected_next_title()}</h3>
  <ol class="next" aria-labelledby="{id}-next">
    {#if agent.mandate}
      <li>{m.connected_next_mandate({ mandate: isolate(mandate) })}</li>
      <li>
        {#if unknown}
          <Banner kind="warning" body={m.connected_approvers_unknown()} />
        {:else if nobody}
          {#if coverage?.normal === 'nobody'}<Banner kind="warning" body={m.connected_approvers_nobody()} />{/if}
          {#if coverage?.critical === 'nobody'}<Banner kind="warning" body={m.connected_approvers_nobody_critical()} />{/if}
        {:else if coverage}
          {notNeeded ? m.connected_approvers_not_needed() : m.connected_approvers_ok()}
        {:else}
          {m.common_loading()}
        {/if}
        {#if unknown || nobody}<a class="setup" href={href({ name: 'settings', section: 'approvers' })}>{m.pair_approvers_setup()}</a>{/if}
      </li>
    {/if}
    <li>{m.connected_try()}</li>
  </ol>
</section>

<style>
  .done {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-3);
    padding: var(--hm-space-6);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .icon {
    display: flex;
    padding: 12px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-positive-bg);
    color: var(--hm-color-positive-fg);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  h2:focus {
    outline: none;
  }
  h3 {
    margin: var(--hm-space-3) 0 0;
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .links {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
  .links a {
    display: inline-flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    color: var(--hm-color-text);
    font-weight: var(--hm-font-weight-semibold);
    text-decoration: none;
  }
  .links a.primary {
    background: var(--hm-color-accent);
    border-color: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .next {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    margin: 0;
    padding-inline-start: var(--hm-space-5);
    inline-size: 100%;
    text-wrap: pretty;
  }
  .next li {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-2);
  }
  .setup {
    display: inline-flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  a:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
</style>
