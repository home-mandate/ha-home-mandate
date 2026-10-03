<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The name an agent claims: untrusted (design README section 7). Cleaned (no control or
  bidi characters, no line breaks), isolated with <bdi>, dotted underline and the title
  "agent's claim, unverified"; screen readers hear ", unverified" after the name, since a
  title on plain text is not announced. With `client` the client identity follows: a checked
  domain in mono, or a paired agent's own identifier marked "unverified".
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { cleanUntrusted } from '../untrusted.ts';
  import ClientIdentity from './ClientIdentity.svelte';

  interface Props {
    name: string;
    /** OAuth client ID; its identity is shown when set. */
    client?: string;
    /** The server's client_verified flag, where known. */
    verified?: boolean;
  }

  let { name, client, verified }: Props = $props();

  // Outside the <bdi>, so the name itself stays exactly what the agent claims. It starts with
  // the comma, not a space: accessible names drop whitespace at the edge of a child.
  const hint = `, ${m.agent_claim_short()}`;
</script>

<span class="agent"
  ><bdi class="name" title={m.agent_claim_label()}>{cleanUntrusted(name)}</bdi
  ><span class="hm-visually-hidden">{hint}</span>{#if client !== undefined}<ClientIdentity {client} {verified} />{/if}</span
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
