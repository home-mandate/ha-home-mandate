<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Browser sign-in (design README 6.2, view=browser): three steps, the MCP address to copy
  and an example for Claude Code. The agent then appears in the list by itself (live
  update); Home-Mandate never shows credentials.
-->
<script lang="ts">
  import { claudeCommand } from '../agents/connect.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import BackLink from '../components/BackLink.svelte';
  import Banner from '../components/Banner.svelte';
  import CopyField from '../components/CopyField.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import { m } from '../i18n.ts';
  import { href } from '../router.ts';

  interface Props {
    app: AppState;
    copy?: (text: string) => Promise<void>;
  }

  let { app, copy }: Props = $props();

  const url = $derived(app.system?.mcp_url ?? null);
  // The example command for Claude Code: only for a plainly formed address, quoted.
  const example = $derived(url ? claudeCommand(url) : null);
</script>

<BackLink href={href({ name: 'agents' })} label={m.agents_title()} />
<PageHeader title={m.browser_title()} />

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
