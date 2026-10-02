<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The name an agent claims: untrusted (design README section 7). Cleaned (no control or
  bidi characters, no line breaks), isolated with <bdi>, dotted underline and the title
  "agent's claim, unverified". With `client` the client identity follows: a checked domain
  in mono, or a paired agent's own identifier marked "unverified".
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import { clientIdentity } from '../ui/identity.ts';

  interface Props {
    name: string;
    /** OAuth client ID; its identity is shown when set. */
    client?: string;
  }

  let { name, client }: Props = $props();

  const identity = $derived(client === undefined ? null : clientIdentity(client));
</script>

<span class="agent"
  ><bdi class="name" title={m.agent_claim_label()}>{cleanUntrusted(name)}</bdi
  >{#if identity?.verified}<span class="domain">{identity.text}</span
    >{:else if identity}<span class="claimed"><bdi>{identity.text}</bdi> <span class="tag">{m.agent_claim_short()}</span></span
    >{/if}</span
>

<style>
  .agent {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 0 var(--hm-space-2);
    min-inline-size: 0;
  }
  .name {
    text-decoration: underline dotted var(--hm-color-text-subtle);
    text-underline-offset: 4px;
    overflow-wrap: anywhere;
  }
  .domain {
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
    overflow-wrap: anywhere;
  }
  .claimed {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
    overflow-wrap: anywhere;
  }
  .tag {
    display: inline-block;
    padding-inline: 6px;
    border-radius: var(--hm-radius-sm);
    border: var(--hm-border-width) dotted var(--hm-color-border-strong);
    font-size: 12px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text-muted);
  }
</style>
