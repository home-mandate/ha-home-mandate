<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Effective changes between two versions of a mandate (save summary, version compare):
  what is newly allowed, newly needs approval, newly denied, each as "from → to". Changes
  that allow more come first within a group and are set in bold.
-->
<script lang="ts">
  import type { Device, MandateDraft } from '../../api/types.ts';
  import { diff, type Change } from '../../engine/analysis.ts';
  import { m } from '../../i18n.ts';
  import { effectGroups } from '../../mandate/changes.ts';
  import { actionLabel } from '../../mandate/labels.ts';
  import { cellText } from '../../mandate/summary.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    prev: MandateDraft;
    next: MandateDraft;
    devices: readonly Device[];
    locale: string;
    /** The device list could not be loaded: the effect is unknown, not "none". */
    unknown?: boolean;
  }

  let { prev, next, devices, locale, unknown = false }: Props = $props();

  /** A longer list is cut and the count of the rest is given; critical actions are never cut. */
  const MAX_ITEMS = 50;
  const shown = (changes: readonly Change[]) => Math.max(MAX_ITEMS, changes.filter((c) => c.to.critical).length);
  const TITLES = {
    allow: () => m.diff_new_allowed(),
    ask: () => m.diff_new_ask(),
    deny: () => m.diff_new_denied(),
  } as const;

  const groups = $derived(effectGroups(diff(prev, next, devices)));
  const count = (n: number) => new Intl.NumberFormat(locale).format(n);
</script>

{#if unknown || devices.length === 0}
  <p class="none warn"><Icon name="warning" size={16} /><span>{m.save_effect_unknown()}</span></p>
{:else if groups.length === 0}
  <p class="none">{m.save_no_effect()}</p>
{:else}
  {#each groups as group (group.kind)}
    <div class="group">
      <span class="title {group.kind}"><Icon name={group.kind} size={16} />{TITLES[group.kind]()} · {count(group.changes.length)}</span>
      <ul role="list">
        {#each group.changes.slice(0, shown(group.changes)) as change (`${change.device.entity_id}/${change.action}`)}
          <li class:widening={change.widening}>
            <bdi>{cleanUntrusted(change.device.name) || cleanUntrusted(change.device.entity_id)}</bdi> · {actionLabel(change.device.category, change.action)}
            <span class="fromto">({cellText(change.from)} → {cellText(change.to)})</span>
          </li>
        {/each}
        {#if group.changes.length > shown(group.changes)}<li class="more">{m.save_more({ count: group.changes.length - shown(group.changes) })}</li>{/if}
      </ul>
    </div>
  {/each}
{/if}

<style>
  .none {
    margin: 0;
    padding-block: var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .warn {
    display: flex;
    gap: 6px;
    color: var(--hm-color-warning-fg);
  }
  .warn :global(svg) {
    flex-shrink: 0;
    margin-block-start: 2px;
  }
  .group {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    padding-block: var(--hm-space-2);
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .title {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
  }
  .allow {
    color: var(--hm-color-allow-fg);
  }
  .ask {
    color: var(--hm-color-ask-fg);
  }
  .deny {
    color: var(--hm-color-deny-fg);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
  }
  li {
    font-size: var(--hm-font-size-sm);
    overflow-wrap: anywhere;
  }
  .widening {
    font-weight: var(--hm-font-weight-semibold);
  }
  .fromto {
    color: var(--hm-color-text-muted);
    font-weight: var(--hm-font-weight-regular);
  }
  .more {
    color: var(--hm-color-text-muted);
  }
</style>
