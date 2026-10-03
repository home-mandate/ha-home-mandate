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
  import ClientIdentity from './ClientIdentity.svelte';

  interface Props {
    name: string;
    /** OAuth client ID; its identity is shown when set. */
    client?: string;
  }

  let { name, client }: Props = $props();
</script>

<span class="agent"
  ><bdi class="name" title={m.agent_claim_label()}>{cleanUntrusted(name)}</bdi
  >{#if client !== undefined}<ClientIdentity {client} />{/if}</span
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
</style>
