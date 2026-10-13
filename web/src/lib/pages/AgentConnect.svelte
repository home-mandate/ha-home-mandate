<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Browser sign-in (design README 6.2, view=browser): three steps, the MCP address to copy
  and an example for Claude Code; Home-Mandate never shows credentials. An agent admitted
  through the browser sign-in while the page is open appears here with the next steps
  (#35), the focus on it; the steps stay below for another agent. The agents there when
  the page opened are never shown, nor agents paired by code.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { claudeCommand } from '../agents/connect.ts';
  import type { Agent } from '../api/types.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import AgentConnected from '../components/agents/AgentConnected.svelte';
  import BackLink from '../components/BackLink.svelte';
  import Banner from '../components/Banner.svelte';
  import CopyField from '../components/CopyField.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { m } from '../i18n.ts';
  import { href } from '../router.ts';
  import { coalesce, RELOAD_WAIT_MS } from '../ui/coalesce.ts';

  interface Props {
    app: AppState;
    copy?: (text: string) => Promise<void>;
  }

  let { app, copy }: Props = $props();

  const url = $derived(app.system?.mcp_url ?? null);
  // The example command for Claude Code: only for a plainly formed address, quoted.
  const example = $derived(url ? claudeCommand(url) : null);

  const id = $props.id();
  const headingId = `${id}-connected`;
  /** When the page opened, on the server's clock: agents created since then are new. */
  let openedAt = 0;
  /** The client IDs seen so far; null until the first list arrived. */
  let known: Set<string> | null = null;
  /** Counts the loads; only the newest one's answer is used. */
  let round = 0;
  /** The newest agent admitted through the browser sign-in since the page opened. */
  let admitted: Agent | null = $state.raw(null);

  const isNew = (a: Agent) => Date.parse(a.created_at) >= openedAt;

  /** look compares the agents with those seen before; a failed load waits for the next event. */
  async function look() {
    const mine = ++round;
    let list: Agent[];
    try {
      list = await app.api.agents();
    } catch {
      return;
    }
    if (mine !== round) return;
    // Taken late (the first load failed or was overtaken), the start still leaves out
    // the agents created since the page opened.
    const seen = known ?? new Set(list.filter((a) => !isNew(a)).map((a) => a.client_id));
    const fresh = list
      .filter((a) => !seen.has(a.client_id) && a.status === 'active' && a.redirect_uris.length > 0)
      .sort((a, b) => b.created_at.localeCompare(a.created_at));
    known = new Set([...seen, ...list.map((a) => a.client_id)]);
    const [newest] = fresh;
    if (!newest) {
      // The shown agent may have changed meanwhile; revoked or removed, it is no longer connected.
      const shown = admitted && list.find((a) => a.client_id === admitted?.client_id);
      admitted = shown && shown.status === 'active' ? shown : null;
      return;
    }
    admitted = newest;
    await tick();
    document.getElementById(headingId)?.focus();
  }

  const later = coalesce(() => void look(), RELOAD_WAIT_MS);
  onMount(() => {
    openedAt = app.serverNow();
    const stop = [app.on('agents.changed', later.call), app.on('mandates.changed', later.call), app.on('reconnected', later.call)];
    void look();
    return () => {
      later.cancel();
      stop.forEach((off) => off());
    };
  });
</script>

<BackLink href={href({ name: 'agents' })} label={m.agents_title()} />
<PageHeader title={m.browser_title()} />

{#if admitted}
  <div class="connected">
    <AgentConnected agent={admitted} {headingId} approvers={(mandateId) => app.api.mandateApprovers(mandateId)} />
  </div>
{/if}

<ol class="steps" role="list">
  <li>
    <span class="n" aria-hidden="true">1</span>
    <div class="body">
      <span class="step">{m.browser_step1()}</span>
      {#if url}
        <CopyField label={m.mcp_endpoint_label()} value={url} help={m.mcp_endpoint_help()} {copy} />
        {#if example}
          <details>
            <summary>{m.browser_example()}</summary>
            <pre dir="ltr">{example}</pre>
          </details>
        {/if}
      {:else}
        <Banner kind="warning" body={m.browser_no_url()} />
      {/if}
    </div>
  </li>
  <li>
    <span class="n" aria-hidden="true">2</span>
    <div class="body"><span class="step">{m.browser_step2()}</span></div>
  </li>
  <li>
    <span class="n" aria-hidden="true">3</span>
    <div class="body"><span class="step">{m.browser_step3()}</span></div>
  </li>
</ol>

<p class="note">{m.browser_note()}</p>
<p class="note">{m.browser_waiting()}</p>

<style>
  .connected {
    margin-block-end: var(--hm-space-5);
  }
  .steps {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    gap: 14px;
    padding: var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .n {
    display: grid;
    place-items: center;
    flex: none;
    inline-size: 28px;
    block-size: 28px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-sunken);
    font-weight: var(--hm-font-weight-semibold);
    font-variant-numeric: tabular-nums;
  }
  .body {
    display: flex;
    flex-direction: column;
    gap: 10px;
    flex: 1;
    min-inline-size: 0;
  }
  .step {
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  summary {
    display: flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    cursor: pointer;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  pre {
    margin: var(--hm-space-2) 0 0;
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border);
    font-family: var(--hm-font-mono);
    font-size: 13px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .note {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
</style>
