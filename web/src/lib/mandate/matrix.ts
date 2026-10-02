// SPDX-License-Identifier: AGPL-3.0-or-later

// The preview "what it may do" as data: one row per device, one cell per action, grouped
// by area or category, with the decision of the draft, the decision of the stored version
// and whether they differ. Decisions come from engine/analysis.ts; the server decides
// every real request itself.

import type { Category, Device, DeviceCatalog, MandateDraft } from '../api/types.ts';
import { cell, type Cell, type CellDecision } from '../engine/analysis.ts';
import { actionsOf, CATEGORIES } from '../engine/vocabulary.ts';
import { cleanUntrusted } from '../untrusted.ts';
import { categoryLabel } from './labels.ts';

export interface MatrixCell {
  action: string;
  cell: Cell;
  /** The same cell in the stored version. */
  previous: Cell;
  changed: boolean;
}

export interface MatrixRow {
  device: Device;
  cells: MatrixCell[];
  changed: boolean;
}

export type Tally = Record<CellDecision, number>;
export type GroupBy = 'area' | 'category';

export interface MatrixBlock {
  category: Category;
  /** Columns: the category's actions that at least one device of the block has. */
  actions: string[];
  rows: MatrixRow[];
}

export interface MatrixGroup {
  /** Area id, category, or "" for devices without an area. */
  key: string;
  blocks: MatrixBlock[];
  devices: number;
  tally: Tally;
}

const sameOutcome = (a: Cell, b: Cell) => a.decision === b.decision && a.timed?.decision === b.timed?.decision;

/** buildRows evaluates every device and action of the catalog, in vocabulary order. */
export function buildRows(draft: MandateDraft, previous: MandateDraft, devices: readonly Device[]): MatrixRow[] {
  return devices.map((device) => {
    const cells = actionsOf(device.category)
      .filter((action) => device.actions.includes(action))
      .map((action): MatrixCell => {
        const now = cell(draft, device, action);
        const before = cell(previous, device, action);
        return { action, cell: now, previous: before, changed: !sameOutcome(now, before) };
      });
    return { device, cells, changed: cells.some((c) => c.changed) };
  });
}

/** tally counts the cells per decision that always applies. */
export function tally(rows: readonly MatrixRow[]): Tally {
  const counts: Tally = { allow: 0, ask: 0, deny: 0, default: 0 };
  for (const row of rows) for (const c of row.cells) counts[c.cell.decision]++;
  return counts;
}

export interface RowFilter {
  /** Matches device name, entity_id, area name and category name. */
  query: string;
  onlyChanges: boolean;
}

/** filterRows keeps the rows that match the search text and, if asked, have a change. */
export function filterRows(rows: readonly MatrixRow[], filter: RowFilter, catalog: DeviceCatalog): MatrixRow[] {
  const query = filter.query.trim().toLowerCase();
  const areaName = new Map(catalog.areas.map((a) => [a.id, a.name]));
  return rows.filter((row) => {
    if (filter.onlyChanges && !row.changed) return false;
    if (query === '') return true;
    const d = row.device;
    const text = [cleanUntrusted(d.name), d.entity_id, cleanUntrusted(areaName.get(d.area ?? '')), categoryLabel(d.category)].join(' ');
    return text.toLowerCase().includes(query);
  });
}

function block(category: Category, rows: MatrixRow[]): MatrixBlock {
  const actions = actionsOf(category).filter((a) => rows.some((r) => r.cells.some((c) => c.action === a)));
  return { category, actions, rows };
}

function group(key: string, rows: MatrixRow[]): MatrixGroup {
  const blocks = CATEGORIES.map((category) => block(category, rows.filter((r) => r.device.category === category))).filter((b) => b.rows.length > 0);
  return { key, blocks, devices: rows.length, tally: tally(rows) };
}

/** groupRows groups by area (in catalog order, devices without an area last) or by category. */
export function groupRows(rows: readonly MatrixRow[], by: GroupBy, catalog: DeviceCatalog): MatrixGroup[] {
  const keys = by === 'category' ? CATEGORIES : [...catalog.areas.map((a) => a.id), ''];
  const known = new Set<string>(keys);
  const keyOf = (row: MatrixRow) => {
    if (by === 'category') return row.device.category;
    return row.device.area !== null && known.has(row.device.area) ? row.device.area : '';
  };
  return keys.map((key) => group(key, rows.filter((r) => keyOf(r) === key))).filter((g) => g.devices > 0);
}

/** cellAt finds a row's cell for a column; a device may lack an action of its block. */
export function cellAt(row: MatrixRow, action: string): MatrixCell | undefined {
  return row.cells.find((c) => c.action === action);
}

export interface GridPosition {
  row: number;
  col: number;
}

const PAGE_ROWS = 10;
const clamp = (value: number, max: number) => Math.min(Math.max(value, 0), max);

/**
 * gridMove returns where a key moves the focus in a grid (design README section 7), or
 * null for keys the grid does not handle. Horizontal arrows are mirrored right-to-left.
 */
export function gridMove(
  key: string,
  at: GridPosition,
  size: { rows: number; cols: number },
  options: { rtl: boolean; ctrl: boolean },
): GridPosition | null {
  const lastRow = size.rows - 1;
  const lastCol = size.cols - 1;
  const forward = options.rtl ? -1 : 1;
  const to = (row: number, col: number): GridPosition => ({ row: clamp(row, lastRow), col: clamp(col, lastCol) });
  switch (key) {
    case 'ArrowRight':
      return to(at.row, at.col + forward);
    case 'ArrowLeft':
      return to(at.row, at.col - forward);
    case 'ArrowDown':
      return to(at.row + 1, at.col);
    case 'ArrowUp':
      return to(at.row - 1, at.col);
    case 'PageDown':
      return to(at.row + PAGE_ROWS, at.col);
    case 'PageUp':
      return to(at.row - PAGE_ROWS, at.col);
    case 'Home':
      return options.ctrl ? to(0, 0) : to(at.row, 0);
    case 'End':
      return options.ctrl ? to(lastRow, lastCol) : to(at.row, lastCol);
    default:
      return null;
  }
}
