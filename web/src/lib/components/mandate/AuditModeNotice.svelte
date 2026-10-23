<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Audit mode of a mandate (SPEC-v0 sections 4.6 and 11.6). A version that switches it on
  waits for a human's confirmation: the notice names who proposed it, when, and its
  digest, which the confirmation sends back so that it counts for exactly this version.
  While audit mode is in force the notice says that denials are not enforced, links to
  those requests in the log, and warns that reading is not protected.
-->
<script lang="ts">
  import type { PendingAuditMode } from '../../api/types.ts';
  import { formatDateTime, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    /** The version in force does not enforce its denials. */
    active: boolean;
    pending: PendingAuditMode | null;
    ctx: FormatContext;
    busy: boolean;
    /** Answers to the proposal; absent where nobody may answer (a revoked mandate). */
    onconfirm?: (digest: string) => void;
    ondiscard?: () => void;
    /** The log filtered to the requests that were not enforced. */
    logHref: string;
    /** What the waiting version changes besides audit mode, one line each; empty for nothing. */
    changes: readonly string[];
  }

  let { active, pending, ctx, busy, onconfirm, ondiscard, logHref, changes }: Props = $props();

  const id = $props.id();
  /** The digest as people compare it: its beginning, enough to tell versions apart. */
  const short = (digest: string) => digest.slice(0, 'sha256:'.length + 12);
  const proposer = $derived(pending ? cleanUntrusted(pending.proposed_by_name ?? '') || cleanUntrusted(pending.proposed_by) : '');
</script>

{#if pending}
  <section class="notice pending" aria-labelledby="{id}-pending">
    <h2 id="{id}-pending"><Icon name="warning" />{m.audit_pending_title()}</h2>
    <p>
      {m.audit_pending_body({ name: isolate(proposer), time: formatDateTime(new Date(pending.proposed_at), ctx), digest: short(pending.digest) })}
    </p>
    {#if changes.length > 0}
      <p>{m.audit_pending_more()}</p>
      <ul>
        {#each changes as line, i (i)}<li><bdi>{line}</bdi></li>{/each}
      </ul>
    {:else}
      <p>{m.audit_pending_only()}</p>
    {/if}
    <p class="note">{m.audit_switch_description()}</p>
    <p class="note warning"><Icon name="warning" size={16} /><span>{m.audit_read_warning()}</span></p>
    {#if onconfirm && ondiscard}
      <div class="actions">
        <Button variant="danger" {busy} onclick={() => onconfirm(pending.digest)}>{m.audit_pending_confirm()}</Button>
        <Button variant="text" disabled={busy} onclick={ondiscard}>{m.audit_pending_discard()}</Button>
      </div>
    {/if}
  </section>
{/if}
{#if active}
  <section class="notice active" aria-labelledby="{id}-active">
    <h2 id="{id}-active"><Icon name="info" />{m.audit_active_title()}</h2>
    <p>{m.audit_active_body()}</p>
    <p class="note warning"><Icon name="warning" size={16} /><span>{m.audit_read_warning()}</span></p>
    <a href={logHref}>{m.audit_active_action()}</a>
  </section>
{/if}

<style>
  .notice {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
    background: var(--hm-color-warning-bg);
    overflow-wrap: anywhere;
  }
  h2 {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-warning-fg);
  }
  p {
    margin: 0;
  }
  .note {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .note.warning {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-1);
    color: var(--hm-color-warning-fg);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
  ul {
    margin: 0;
    padding-inline-start: var(--hm-space-4);
  }
</style>
