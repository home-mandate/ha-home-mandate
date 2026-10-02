<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Status at a glance (design README 6.1): Home Assistant, emergency stop, active agents and
  pending approvals. A tile in danger has a coloured border, not only a coloured value.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import Icon, { type IconName } from '../Icon.svelte';

  export interface Tile {
    label: string;
    icon: IconName;
    value: string;
    hint: string;
    tone: 'positive' | 'danger' | 'accent' | 'ask' | 'muted';
  }

  interface Props {
    /** null while loading. */
    tiles: Tile[] | null;
  }

  let { tiles }: Props = $props();

  const SKELETONS = [1, 2, 3, 4];
</script>

<section class="tiles" aria-label={m.overview_status_heading()} aria-busy={tiles === null}>
  {#if tiles === null}
    {#each SKELETONS as n (n)}<div class="tile"><span class="bone"></span><span class="bone wide"></span><span class="bone"></span></div>{/each}
  {:else}
    {#each tiles as tile (tile.label)}
      <div class="tile {tile.tone}">
        <span class="label">{tile.label}</span>
        <div class="value"><Icon name={tile.icon} /><span>{tile.value}</span></div>
        <span class="hint">{tile.hint}</span>
      </div>
    {/each}
  {/if}
</section>

<style>
  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 150px), 1fr));
    gap: var(--hm-space-3);
  }
  .tile {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    padding: var(--hm-space-4);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    min-inline-size: 0;
  }
  .label,
  .hint {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .value {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    font-size: var(--hm-font-size-xl);
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .positive .value {
    color: var(--hm-color-positive-fg);
  }
  .accent .value {
    color: var(--hm-color-accent-text);
  }
  .muted .value {
    color: var(--hm-color-text-subtle);
  }
  .danger {
    border-color: var(--hm-color-danger-border);
  }
  .danger .value {
    color: var(--hm-color-danger-fg);
  }
  .ask {
    border-color: var(--hm-color-ask-border);
  }
  .ask .value {
    color: var(--hm-color-ask-fg);
  }
  .bone {
    display: block;
    block-size: 12px;
    inline-size: 50%;
    border-radius: var(--hm-radius-sm);
    background: var(--hm-color-skeleton);
  }
  .bone.wide {
    block-size: 22px;
    inline-size: 70%;
  }
</style>
