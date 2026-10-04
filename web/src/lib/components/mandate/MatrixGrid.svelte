<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One block of the preview matrix (design README 6.6): devices of one category as rows,
  the category's actions as columns. A cell is icon over word, never the icon alone; the
  default has a dashed border. Corner marks: shield = downgraded, clock = depends on time,
  dot = changed since the stored version.

  Keyboard (README section 7): one cell is in the tab order (roving tabindex); arrows move
  (mirrored right-to-left), Home/End within the row, Ctrl+Home/End to the corners, Page
  keys by ten rows. The selection follows the focus; the explanation is shown above.
-->
<script lang="ts">
  import { isCritical } from '../../engine/vocabulary.ts';
  import { m } from '../../i18n.ts';
  import { cellLabel, shortLabel } from '../../mandate/explain.ts';
  import { actionLabel } from '../../mandate/labels.ts';
  import { cellAt, gridMove, type MatrixBlock } from '../../mandate/matrix.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    block: MatrixBlock;
    /** Accessible name of the grid, e.g. "Kitchen · Lights". */
    label: string;
    /** Text above the device column; empty when the group already names the category. */
    heading: string;
    /** Key of the selected cell: "entity_id|action". */
    selected: string | null;
    onselect: (key: string) => void;
  }

  let { block, label, heading, selected, onselect }: Props = $props();

  let grid: HTMLElement | undefined = $state();

  const keyOf = (entityId: string, action: string) => `${entityId}|${action}`;
  /** The cell in the tab order: the selected one if it is in this block, else the first. */
  const tabStop = $derived.by(() => {
    const here = block.rows.some((row) => row.cells.some((c) => keyOf(row.device.entity_id, c.action) === selected));
    const first = block.rows[0];
    return here ? selected : first?.cells[0] ? keyOf(first.device.entity_id, first.cells[0].action) : null;
  });

  const cellAtPosition = (row: number, col: number) => grid?.querySelector<HTMLElement>(`[data-row="${row}"][data-col="${col}"]`) ?? null;

  function keydown(event: KeyboardEvent, row: number, col: number, key: string) {
    // A gridcell is no button (ARIA in HTML, review a11y L8): Enter and Space select it.
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onselect(key);
      return;
    }
    const rtl = grid ? getComputedStyle(grid).direction === 'rtl' : false;
    const size = { rows: block.rows.length, cols: block.actions.length };
    const to = gridMove(event.key, { row, col }, size, { rtl, ctrl: event.ctrlKey || event.metaKey });
    if (!to) return;
    event.preventDefault();
    // A device may lack an action of its block. An arrow then goes on to the next cell
    // that exists; a jump (Home, End, page keys) stops at the last one before the hole.
    const step = event.key.startsWith('Arrow') ? 1 : -1;
    const rowStep = Math.sign(to.row - row) * step;
    const colStep = Math.sign(to.col - col) * step;
    let r = to.row;
    let c = to.col;
    let target = cellAtPosition(r, c);
    while (!target && (rowStep !== 0 || colStep !== 0)) {
      r += rowStep;
      c += colStep;
      if (r < 0 || r >= size.rows || c < 0 || c >= size.cols || (r === row && c === col)) return;
      target = cellAtPosition(r, c);
    }
    if (!target) return;
    target.focus();
    target.click();
  }
</script>

<div bind:this={grid} class="grid" role="grid" aria-label={label} style:--columns={block.actions.length}>
  <div role="row">
    <div role="columnheader" class="corner">{heading}<span class="hm-visually-hidden">{m.matrix_col_device()}</span></div>
    {#each block.actions as action (action)}
      <div role="columnheader" class="action">
        {actionLabel(block.category, action)}
        {#if isCritical(block.category, action)}<span class="shield"><Icon name="critical" size={16} label={m.critical_label()} /></span>{/if}
      </div>
    {/each}
  </div>
  {#each block.rows as row, r (row.device.entity_id)}
    <div role="row">
      <div role="rowheader" class="device">
        <bdi>{cleanUntrusted(row.device.name) || cleanUntrusted(row.device.entity_id)}</bdi>
        {#if row.device.critical}<span class="shield"><Icon name="critical" size={16} label={m.critical_device_label()} /></span>{/if}
        <code title={cleanUntrusted(row.device.entity_id)}>{cleanUntrusted(row.device.entity_id)}</code>
      </div>
      {#each block.actions as action, c (action)}
        {@const cell = cellAt(row, action)}
        {#if cell}
          {@const key = keyOf(row.device.entity_id, action)}
          {@const name = cellLabel(row.device, cell)}
          <div
            role="gridcell"
            class="cell {cell.cell.decision}"
            aria-selected={selected === key}
            aria-label={name}
            title={name}
            tabindex={tabStop === key ? 0 : -1}
            data-row={r}
            data-col={c}
            onclick={() => onselect(key)}
            onkeydown={(e) => keydown(e, r, c, key)}
          >
            <Icon name={cell.cell.demoted ? 'demoted' : cell.cell.decision} size={16} />
            <span>{shortLabel(cell.cell.decision)}</span>
            {#if cell.cell.demoted}<span class="mark end critical"><Icon name="critical" size={16} /></span>{/if}
            {#if cell.cell.timed}<span class="mark start"><Icon name="history" size={16} /></span>{/if}
            {#if cell.changed}<span class="dot"></span>{/if}
          </div>
        {:else}
          <div role="gridcell" class="hole" aria-label={m.matrix_cell_none()}></div>
        {/if}
      {/each}
    </div>
  {/each}
</div>

<style>
  .grid {
    display: grid;
    grid-template-columns: minmax(150px, 1.6fr) repeat(var(--columns), minmax(66px, 1fr));
    gap: var(--hm-space-1);
    min-inline-size: calc(150px + var(--columns) * 70px);
  }
  /* Rows are real boxes that take over the grid's columns: "display: contents" would
     drop the row from the accessibility tree in some browsers. */
  [role='row'] {
    display: grid;
    grid-column: 1 / -1;
    grid-template-columns: subgrid;
  }
  .corner,
  .device {
    position: sticky;
    inset-inline-start: 0;
    z-index: 1;
    background: var(--hm-color-surface);
  }
  [role='columnheader'] {
    display: flex;
    align-items: flex-end;
    gap: 3px;
    padding: var(--hm-space-1) 2px;
    font-size: 12px;
    font-weight: var(--hm-font-weight-medium);
    line-height: var(--hm-line-height-tight);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  .corner {
    padding-inline: 0;
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
  }
  .shield {
    display: flex;
    color: var(--hm-color-critical-fg);
  }
  .shield :global(svg) {
    inline-size: 12px;
    block-size: 12px;
  }
  .device {
    display: flex;
    flex-direction: column;
    justify-content: center;
    min-inline-size: 0;
    padding-block: var(--hm-space-1);
    padding-inline: 0 var(--hm-space-2);
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
    font-size: var(--hm-font-size-sm);
    line-height: 1.3;
    overflow-wrap: anywhere;
  }
  code {
    font-size: 11px;
    color: var(--hm-color-text-subtle);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .cell {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1px;
    min-block-size: 48px;
    padding: var(--hm-space-1) 2px;
    box-sizing: border-box;
    border-radius: 6px;
    border: var(--hm-border-width) solid;
    font: inherit;
    font-size: 12px;
    font-weight: var(--hm-font-weight-medium);
    line-height: 1.2;
    text-align: center;
    overflow-wrap: anywhere;
    cursor: pointer;
  }
  .cell {
    /* Keeps a focused cell clear of the sticky device column when the grid scrolls. */
    scroll-margin-inline-start: 160px;
  }
  .cell:focus-visible {
    outline-offset: 1px;
  }
  .cell[aria-selected='true'] {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 1px;
  }
  .allow {
    color: var(--hm-color-allow-fg);
    background: var(--hm-color-allow-bg);
    border-color: var(--hm-color-allow-border);
  }
  .ask {
    color: var(--hm-color-ask-fg);
    background: var(--hm-color-ask-bg);
    border-color: var(--hm-color-ask-border);
  }
  .deny {
    color: var(--hm-color-deny-fg);
    background: var(--hm-color-deny-bg);
    border-color: var(--hm-color-deny-border);
  }
  .default {
    color: var(--hm-color-default-fg);
    background: var(--hm-color-default-bg);
    border-color: var(--hm-color-default-border);
    border-style: dashed;
  }
  .mark {
    position: absolute;
    inset-block-start: -5px;
    display: flex;
    padding: 1px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface);
    color: var(--hm-color-text-muted);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  .mark :global(svg) {
    inline-size: 11px;
    block-size: 11px;
  }
  .mark.end {
    inset-inline-end: -5px;
  }
  .mark.start {
    inset-inline-start: -5px;
  }
  .mark.critical {
    color: var(--hm-color-critical-fg);
    border-color: var(--hm-color-critical-border);
  }
  .dot {
    position: absolute;
    inset-block-end: 3px;
    inset-inline-end: 3px;
    inline-size: 7px;
    block-size: 7px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-accent);
    box-shadow: 0 0 0 1.5px var(--hm-color-surface);
  }
  .hole {
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  @media (forced-colors: active) {
    .cell[aria-selected='true'] {
      outline-color: Highlight;
    }
    .dot {
      background: Highlight;
      forced-color-adjust: none;
    }
  }
</style>
