<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Preview "what it may do" (design README 6.6), computed live from the draft with the UI's
  own evaluation; the server decides every real request itself. Groups by area or category
  are collapsible and carry the counts per decision, so one can scan without opening. On
  desktop each block is a grid and a selected cell is explained above; on mobile there is
  no grid: one card per device with a list "action → decision".
-->
<script lang="ts">
  import type { DeviceCatalog, MandateDraft } from '../../api/types.ts';
  import type { CellDecision } from '../../engine/analysis.ts';
  import { m } from '../../i18n.ts';
  import { cellDetail, cellWhy, type DetailLine } from '../../mandate/explain.ts';
  import { actionLabel, categoryLabel, decisionLabel } from '../../mandate/labels.ts';
  import { buildRows, filterRows, groupRows, tally, type GroupBy, type MatrixGroup } from '../../mandate/matrix.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import DecisionBadge from '../DecisionBadge.svelte';
  import Icon, { type IconName } from '../Icon.svelte';
  import Switch from '../Switch.svelte';
  import DecisionChip from './DecisionChip.svelte';
  import MatrixGrid from './MatrixGrid.svelte';

  interface Props {
    draft: MandateDraft;
    /** The stored version the draft is compared with. */
    previous: MandateDraft;
    /** Number of the stored version. */
    version: number;
    catalog: DeviceCatalog;
    locale: string;
    /** Grid with a detail panel (desktop) or cards per device (mobile). */
    grid: boolean;
    /** The draft has errors; nothing of it would apply. */
    invalid: boolean;
    /** The device list could not be loaded (as opposed to: there are no devices). */
    catalogMissing?: boolean;
    /** Why the mandate does not apply right now ("Expired"), or "" if it does. */
    notInEffect?: string;
  }

  let { draft, previous, version, catalog, locale, grid, invalid, catalogMissing = false, notInEffect = '' }: Props = $props();

  const id = $props.id();
  /** Rows of a block before "show more". */
  const ROWS_SHOWN = 5;
  /** Groups open at first. */
  const OPEN_AT_FIRST = 2;
  const KINDS: readonly CellDecision[] = ['allow', 'ask', 'deny', 'default'];
  const GROUPINGS: readonly GroupBy[] = ['area', 'category'];
  const GROUPING_LABELS: Record<GroupBy, () => string> = { area: () => m.matrix_group_area(), category: () => m.matrix_group_category() };
  const LINE_ICONS: Record<DetailLine['kind'], IconName> = { rule: 'list', critical: 'critical', danger: 'warning', timed: 'history', changed: 'info' };

  let by = $state<GroupBy>('category');
  let query = $state('');
  let onlyChanges = $state(false);
  /** Groups opened or closed by hand; null: the first ones, or all while filtering. */
  let opened = $state<readonly string[] | null>(null);
  let expanded: readonly string[] = $state([]);
  let selected = $state<string | null>(null);
  const radios: HTMLButtonElement[] = $state([]);

  const rows = $derived(buildRows(draft, previous, catalog.devices));
  const counts = $derived(tally(rows));
  const filtering = $derived(query.trim() !== '' || onlyChanges);
  const visible = $derived(filterRows(rows, { query, onlyChanges }, catalog));
  const groups = $derived(groupRows(visible, by, catalog));
  /** What a filter left, for screen readers; nothing to say without a filter. */
  const found = $derived(!filtering ? '' : visible.length === 0 ? m.preview_no_result() : m.preview_device_count({ count: visible.length }));
  const openKeys = $derived(opened ?? (filtering ? groups.map((g) => g.key) : groups.slice(0, OPEN_AT_FIRST).map((g) => g.key)));
  const number = (n: number) => new Intl.NumberFormat(locale).format(n);

  const chosen = $derived.by(() => {
    for (const row of rows) {
      const cell = row.cells.find((c) => `${row.device.entity_id}|${c.action}` === selected);
      if (cell) return { row, cell, lines: cellDetail(cell, draft, catalog, version, locale) };
    }
    return null;
  });

  function areaName(key: string): string {
    if (key === '') return m.matrix_no_area();
    return cleanUntrusted(catalog.areas.find((a) => a.id === key)?.name) || cleanUntrusted(key);
  }
  const byArea = $derived(by === 'area');
  const groupTitle = (group: MatrixGroup) => (byArea ? areaName(group.key) : categoryLabel(group.key));
  /** Name of a block's grid: with the area in front when the groups are areas. */
  const blockName = (group: MatrixGroup, category: string) => (byArea ? `${groupTitle(group)} · ${categoryLabel(category)}` : categoryLabel(category));
  const blockHeading = (category: string) => (byArea ? categoryLabel(category) : '');

  function regroup(next: GroupBy) {
    by = next;
    opened = null;
  }

  function radioKey(event: KeyboardEvent) {
    const rtl = getComputedStyle(event.currentTarget as Element).direction === 'rtl';
    const forward = ['ArrowDown', rtl ? 'ArrowLeft' : 'ArrowRight'];
    const back = ['ArrowUp', rtl ? 'ArrowRight' : 'ArrowLeft'];
    if (!forward.includes(event.key) && !back.includes(event.key)) return;
    event.preventDefault();
    const index = (GROUPINGS.indexOf(by) + 1) % GROUPINGS.length;
    regroup(GROUPINGS[index] as GroupBy);
    radios[index]?.focus();
  }

  function toggleGroup(key: string) {
    opened = openKeys.includes(key) ? openKeys.filter((k) => k !== key) : [...openKeys, key];
  }

  function shown(group: MatrixGroup, category: string, total: number): number {
    return filtering || expanded.includes(`${by}/${group.key}/${category}`) ? total : Math.min(total, ROWS_SHOWN);
  }
</script>

<section class="preview" aria-labelledby="{id}-title">
  <div class="top">
    <h2 id="{id}-title">{m.matrix_title()}</h2>
    <div class="grouping" role="radiogroup" aria-label={m.matrix_group_by()}>
      {#each GROUPINGS as grouping, i (grouping)}
        <button
          bind:this={radios[i]}
          type="button"
          role="radio"
          aria-checked={by === grouping}
          tabindex={by === grouping ? 0 : -1}
          onclick={() => regroup(grouping)}
          onkeydown={radioKey}
        >
          {GROUPING_LABELS[grouping]()}
        </button>
      {/each}
    </div>
  </div>

  <div class="filters">
    <div class="search">
      <span class="glass"><Icon name="search" /></span>
      <input type="search" bind:value={query} placeholder={m.matrix_filter()} aria-label={m.matrix_filter()} />
    </div>
    <Switch
      checked={onlyChanges}
      label={m.preview_only_changes({ version })}
      onchange={(on) => {
        onlyChanges = on;
        opened = null;
      }}
    />
  </div>

  <div class="tally">
    <span class="total">{m.matrix_count({ count: catalog.devices.length })}:</span>
    {#each KINDS as kind (kind)}
      <DecisionChip {kind}><span class="n">{number(counts[kind])}</span> {decisionLabel(kind)}</DecisionChip>
    {/each}
  </div>

  <p class="hm-visually-hidden" role="status">{found}</p>

  {#if invalid}
    <p class="invalid" role="status"><Icon name="warning" /><span>{m.matrix_invalid()}</span></p>
  {:else if notInEffect}
    <p class="invalid"><Icon name="info" /><span>{m.matrix_not_in_effect({ status: notInEffect })}</span></p>
  {/if}

  {#if grid}
    <div class="detail">
      {#if chosen}
        {@const decision = chosen.cell.cell.decision}
        <div class="verdict">
          <span class="circle {decision}"><Icon name={chosen.cell.cell.demoted ? 'demoted' : decision} /></span>
          <span class="words">
            <span class="where"><bdi>{cleanUntrusted(chosen.row.device.name) || cleanUntrusted(chosen.row.device.entity_id)}</bdi> · {actionLabel(chosen.row.device.category, chosen.cell.action)}</span>
            <span class="label {decision}">{decisionLabel(decision)}</span>
          </span>
        </div>
      {:else}
        <span class="hint">{m.matrix_select_hint()}</span>
      {/if}
      <!-- The cell's own name already says device, action and decision; only the reason is
           announced. The region exists before the first selection, so that one is said too. -->
      <div class="lines" aria-live="polite" aria-atomic="true">
        {#if chosen}
          {#each chosen.lines as line (line.kind)}
            <span class="line {line.kind}"><Icon name={LINE_ICONS[line.kind]} size={16} /><span>{line.text}</span></span>
          {/each}
        {/if}
      </div>
    </div>
  {/if}

  {#if catalogMissing}
    <p class="none">{m.matrix_no_catalog()}</p>
  {:else if catalog.devices.length === 0}
    <p class="none">{m.matrix_no_devices()}</p>
  {:else if groups.length === 0}
    <p class="none">{m.preview_no_result()}</p>
  {/if}

  <div class="groups">
    {#each groups as group (group.key)}
      {@const open = openKeys.includes(group.key)}
      <div class="group">
        <h3 class="group-head">
          <button type="button" class="toggle" aria-expanded={open} onclick={() => toggleGroup(group.key)}>
            <span class="chevron" class:closed={!open}><Icon name="chevronDown" size={16} /></span>
            <span class="title"><bdi>{groupTitle(group)}</bdi></span>
            <span class="count">{m.preview_device_count({ count: group.devices })}</span>
            <span class="mini">
              {#each KINDS.filter((k) => group.tally[k] > 0) as kind (kind)}
                <span class={kind}><Icon name={kind} size={16} label={decisionLabel(kind)} />{number(group.tally[kind])}</span>
              {/each}
            </span>
          </button>
        </h3>
        {#if open}
          <div class="blocks" class:scroll={grid}>
            {#each group.blocks as block (block.category)}
              {@const limit = shown(group, block.category, block.rows.length)}
              {@const shownBlock = { ...block, rows: block.rows.slice(0, limit) }}
              {#if grid}
                <MatrixGrid
                  block={shownBlock}
                  label={blockName(group, block.category)}
                  heading={blockHeading(block.category)}
                  {selected}
                  onselect={(key) => (selected = key)}
                />
              {:else}
                {#if byArea}<h4>{categoryLabel(block.category)}</h4>{/if}
                <ul class="cards" role="list">
                  {#each shownBlock.rows as row (row.device.entity_id)}
                    <li class="card">
                      <span class="device"><bdi>{cleanUntrusted(row.device.name) || cleanUntrusted(row.device.entity_id)}</bdi><code>{cleanUntrusted(row.device.entity_id)}</code></span>
                      <ul class="actions" role="list">
                        {#each row.cells as cell (cell.action)}
                          <li>
                            <span class="action">
                              {actionLabel(row.device.category, cell.action)}
                              {#if cell.changed}<span class="dot" role="img" aria-label={m.preview_change_dot()}></span>{/if}
                            </span>
                            <span class="outcome">
                              <DecisionBadge kind={cell.cell.decision} size="sm" />
                              <span class="why">{cellWhy(cell.cell)}</span>
                            </span>
                          </li>
                        {/each}
                      </ul>
                    </li>
                  {/each}
                </ul>
              {/if}
              {#if block.rows.length > limit}
                <Button variant="text" onclick={() => (expanded = [...expanded, `${by}/${group.key}/${block.category}`])}>
                  {m.preview_show_more({ count: block.rows.length - limit })}
                </Button>
              {/if}
            {/each}
          </div>
        {/if}
      </div>
    {/each}
  </div>

  {#if grid}
    <div class="legend">
      <span class="heading">{m.matrix_legend()}</span>
      <span><span class="critical"><Icon name="critical" size={16} /></span>{m.demoted_label()}</span>
      <span><Icon name="history" size={16} />{m.preview_timed()}</span>
      <span><span class="dot"></span>{m.preview_change_dot()}</span>
    </div>
  {/if}
</section>

<style>
  .preview {
    display: flex;
    flex-direction: column;
    gap: 14px;
    min-inline-size: 0;
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border);
  }
  .top,
  .filters {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
  }
  .filters {
    column-gap: var(--hm-space-4);
  }
  h2 {
    flex: 1 1 200px;
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
  }
  h4 {
    margin: 0;
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text-muted);
  }
  .group-head {
    display: flex;
    margin: 0;
    font: inherit;
  }
  .group-head .toggle {
    flex: 1;
  }
  .grouping {
    display: flex;
    padding: 3px;
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border);
  }
  .grouping button {
    min-block-size: 36px;
    padding-inline: var(--hm-space-3);
    border-radius: 6px;
    border: none;
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text-muted);
    background: transparent;
    cursor: pointer;
  }
  .grouping button[aria-checked='true'] {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    box-shadow: var(--hm-shadow-sm);
  }
  .search {
    position: relative;
    display: flex;
    flex: 1 1 220px;
  }
  .glass {
    position: absolute;
    inset-inline-start: var(--hm-space-3);
    inset-block-start: var(--hm-space-3);
    display: flex;
    color: var(--hm-color-text-subtle);
    pointer-events: none;
  }
  input {
    inline-size: 100%;
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding-block: 0;
    padding-inline: 42px var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  input:focus-visible {
    outline-offset: 0;
  }
  .tally {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    font-size: var(--hm-font-size-xs);
  }
  .total {
    padding-inline-end: var(--hm-space-1);
    color: var(--hm-color-text-subtle);
  }
  .n {
    font-variant-numeric: tabular-nums;
  }
  .invalid {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
    padding: 10px var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-warning-fg);
    background: var(--hm-color-warning-bg);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
  }
  .detail {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3) var(--hm-space-4);
    padding: 14px;
    border-radius: 10px;
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .hint {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .verdict {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .circle {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    inline-size: 40px;
    block-size: 40px;
    border-radius: var(--hm-radius-pill);
    color: var(--hm-color-on-decision);
  }
  .circle.allow {
    background: var(--hm-color-allow-solid);
  }
  .circle.ask {
    background: var(--hm-color-ask-solid);
  }
  .circle.deny {
    background: var(--hm-color-deny-solid);
  }
  .circle.default {
    background: var(--hm-color-default-solid);
  }
  .words {
    display: flex;
    flex-direction: column;
  }
  .where {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
    overflow-wrap: anywhere;
  }
  .label {
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .label.allow,
  .mini .allow {
    color: var(--hm-color-allow-fg);
  }
  .label.ask,
  .mini .ask {
    color: var(--hm-color-ask-fg);
  }
  .label.deny,
  .mini .deny {
    color: var(--hm-color-deny-fg);
  }
  .label.default,
  .mini .default {
    color: var(--hm-color-default-fg);
  }
  .lines {
    flex: 1 1 240px;
    min-inline-size: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    font-size: var(--hm-font-size-sm);
  }
  .line {
    display: flex;
    gap: 6px;
    text-wrap: pretty;
    overflow-wrap: anywhere;
  }
  .line :global(svg) {
    flex-shrink: 0;
    margin-block-start: 2px;
  }
  .line.critical {
    color: var(--hm-color-critical-fg);
  }
  .line.danger {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
  }
  .line.timed {
    color: var(--hm-color-text-muted);
  }
  .line.changed {
    color: var(--hm-color-accent-text);
  }
  .none {
    margin: 0;
    padding-block: var(--hm-space-4);
    font-size: 15px;
    color: var(--hm-color-text-muted);
  }
  .groups {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .group {
    display: flex;
    flex-direction: column;
    border-radius: 10px;
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .toggle {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px var(--hm-space-3);
    min-block-size: 48px;
    padding: var(--hm-space-2) var(--hm-space-3);
    border: none;
    border-radius: 10px;
    font: inherit;
    text-align: start;
    color: var(--hm-color-text);
    background: transparent;
    cursor: pointer;
  }
  .toggle:hover {
    background: var(--hm-color-surface-hover);
  }
  .toggle:focus-visible {
    outline-offset: -2px;
  }
  .chevron {
    display: flex;
    color: var(--hm-color-text-subtle);
  }
  .chevron.closed {
    rotate: -90deg;
  }
  :global([dir='rtl']) .chevron.closed {
    rotate: 90deg;
  }
  .title {
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .count {
    flex: 1;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .mini {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
    font-size: var(--hm-font-size-xs);
    font-variant-numeric: tabular-nums;
  }
  .mini span {
    display: inline-flex;
    align-items: center;
    gap: 3px;
  }
  .blocks {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 14px;
    padding: var(--hm-space-1) var(--hm-space-3) 14px;
  }
  .blocks > :global(*) {
    align-self: stretch;
  }
  .blocks > :global(.btn) {
    align-self: flex-start;
    margin-block-start: calc(-1 * var(--hm-space-2));
  }
  .scroll {
    overflow-x: auto;
    overflow-y: hidden;
    scroll-padding-inline-start: 160px;
  }
  .cards,
  .actions {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
  }
  .cards {
    gap: var(--hm-space-2);
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .device {
    display: flex;
    flex-direction: column;
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  code {
    font-size: 12px;
    font-weight: var(--hm-font-weight-regular);
    color: var(--hm-color-text-subtle);
    overflow-wrap: anywhere;
  }
  .actions li {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hm-space-1) var(--hm-space-3);
    padding-block: 6px;
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
    font-size: var(--hm-font-size-sm);
  }
  .action {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .outcome {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hm-space-1) var(--hm-space-2);
  }
  .why {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .dot {
    display: inline-block;
    inline-size: 8px;
    block-size: 8px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-accent);
  }
  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 14px;
    padding-block-start: 10px;
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .legend span {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
  }
  .heading {
    font-weight: var(--hm-font-weight-semibold);
  }
  .critical {
    color: var(--hm-color-critical-fg);
  }
  @media (pointer: coarse), (max-width: 767px) {
    .grouping button {
      min-block-size: var(--hm-size-touch);
    }
  }
  @media (forced-colors: active) {
    .circle {
      border: 2px solid CanvasText;
    }
    .dot {
      background: Highlight;
      forced-color-adjust: none;
    }
  }
</style>
