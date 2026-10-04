<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  MCP endpoint (design README 6.11 section 4): the address agents connect to, to copy, and
  the TLS certificate. A missing certificate is amber with exact instructions, not red:
  agents on this machine still work.
-->
<script lang="ts">
  import type { SystemStatus } from '../../api/types.ts';
  import { formatDate, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import Banner from '../Banner.svelte';
  import CopyField from '../CopyField.svelte';
  import StatusPill from '../StatusPill.svelte';

  interface Props {
    system: SystemStatus;
    ctx: FormatContext;
  }

  let { system, ctx }: Props = $props();
</script>

{#if system.mcp_url}
  <CopyField label={m.mcp_endpoint_label()} value={system.mcp_url} help={m.mcp_endpoint_help()} />
{/if}
<div class="tls">
  <span class="label">{m.set_tls()}</span>
  {#if system.tls.present}
    <StatusPill tone="positive"
      >{system.tls.valid_until ? m.set_tls_ok({ date: formatDate(new Date(system.tls.valid_until), ctx) }) : m.status_active()}</StatusPill
    >
  {:else}
    <Banner kind="warning" body={m.set_tls_missing()} />
  {/if}
</div>

<style>
  .tls {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-2);
  }
  /* The certificate line is a sentence: it wraps instead of widening the page. */
  .tls :global(.pill) {
    white-space: normal;
    max-inline-size: 100%;
  }
  .label {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
</style>
