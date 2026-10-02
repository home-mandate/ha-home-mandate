<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Filters of the audit log (design README 6.8): period, agent, device or area, event type,
  decision chips and the number of matches (role=status). On mobile the selects fold
  behind a "Filters (n)" button; chips and count stay visible.
-->
<script lang="ts">
  import type { DecisionFilter } from '../../api/types.ts';
  import { DECISIONS, DEFAULT_FILTERS, EVENTS, PERIODS, activeCount, type AuditFilters, type Period, type TypeFilter } from '../../audit/filters.ts';
  import { eventLabel } from '../../audit/outcome.ts';
  import { m } from '../../i18n.ts';
  import { decisionLabel } from '../../mandate/labels.ts';
  import Button from '../Button.svelte';
  import SelectField from '../SelectField.svelte';
  import ToggleChip from '../ToggleChip.svelte';
  import { cleanUntrusted } from '../../untrusted.ts';

  type Option = { value: string; label: string };

  interface Props {
    filters: AuditFilters;
    agents: readonly Option[];
    areas: readonly Option[];
    devices: readonly Option[];
    /** Number of matches; null while loading. */
    total: number | null;
    compact: boolean;
    onchange: (filters: AuditFilters) => void;
  }

  let { filters, agents, areas, devices, total, compact, onchange }: Props = $props();

  const id = $props.id();
  let open = $state(false);

  const PERIOD_LABELS: Record<Period, () => string> = {
    '24h': () => m.filter_period_24h(),
    '7d': () => m.filter_period_7d(),
    all: () => m.filter_period_all(),
  };

  const periods = $derived(PERIODS.map((p) => ({ value: p, label: PERIOD_LABELS[p]() })));
  // A value from the URL that is not among the options is still shown (cleaned), so a
  // crafted link cannot filter the list invisibly.
  const unknownAgent = $derived(filters.agent !== null && !agents.some((a) => a.value === filters.agent));
  const unknownDevice = $derived(
    filters.device !== null && !areas.some((a) => a.value === filters.device) && !devices.some((d) => d.value === filters.device),
  );
  const agentOptions = $derived([
    { value: '', label: m.filter_agent_all() },
    ...(unknownAgent && filters.agent ? [{ value: filters.agent, label: cleanUntrusted(filters.agent) }] : []),
    ...agents,
  ]);
  const deviceOptions = $derived([
    { value: '', label: m.filter_device_all() },
    ...(unknownDevice && filters.device ? [{ value: filters.device, label: cleanUntrusted(filters.device) }] : []),
  ]);
  const types = $derived([{ value: 'all', label: m.filter_types_all() }, ...EVENTS.map((e) => ({ value: e, label: eventLabel(e) }))]);
  const groups = $derived([
    { label: m.filter_areas(), options: areas },
    { label: m.filter_devices(), options: devices },
  ]);
  const count = $derived(activeCount(filters));

  function set(patch: Partial<AuditFilters>) {
    onchange({ ...filters, ...patch, seq: null });
  }

  function toggle(decision: DecisionFilter, on: boolean) {
    const decisions = DECISIONS.filter((d) => (d === decision ? on : filters.decisions.includes(d)));
    set({ decisions });
  }
</script>

<div class="filters" role="search" aria-label={m.filter_label()}>
  {#if compact}
    <button type="button" class="toggle" aria-expanded={open} aria-controls="{id}-fields" onclick={() => (open = !open)}>
      {m.filter_label()}{#if count > 0}<span class="badge" aria-hidden="true">{count}</span><span class="hm-visually-hidden"
          >{m.filter_active_count({ count })}</span
        >{/if}
    </button>
  {/if}
  {#if !compact || open}
    <div class="fields" id="{id}-fields">
      <SelectField label={m.filter_period()} value={filters.period} options={periods} onchange={(v) => set({ period: v as Period })} />
      <SelectField label={m.filter_agent()} value={filters.agent ?? ''} options={agentOptions} onchange={(v) => set({ agent: v || null })} />
      <SelectField
        label={m.filter_device()}
        value={filters.device ?? ''}
        options={deviceOptions}
        {groups}
        onchange={(v) => set({ device: v || null })}
      />
      <SelectField label={m.filter_event_type()} value={filters.type} options={types} onchange={(v) => set({ type: v as TypeFilter })} />
    </div>
  {/if}
  <fieldset>
    <legend>{m.filter_decision()}</legend>
    <div class="chips">
      {#each DECISIONS as decision (decision)}
        <ToggleChip pressed={filters.decisions.includes(decision)} onchange={(on) => toggle(decision, on)}>{decisionLabel(decision)}</ToggleChip>
      {/each}
    </div>
  </fieldset>
  <div class="foot">
    <span role="status">{total === null ? '' : m.filter_results({ count: total })}</span>
    <!-- Always there (disabled without filters), so resetting does not drop the focus. -->
    <Button variant="text" disabled={count === 0} onclick={() => onchange({ ...DEFAULT_FILTERS })}>{m.filter_reset()}</Button>
  </div>
</div>

<style>
  .filters {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr));
    gap: var(--hm-space-3);
  }
  fieldset {
    margin: 0;
    padding: 0;
    border: 0;
    min-inline-size: 0;
  }
  legend {
    margin-block-end: 6px;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .chips {
    display: flex;
    gap: var(--hm-space-2);
    overflow-x: auto;
    /* room for the focus ring inside the scroll container */
    padding: 4px;
    margin: -4px;
  }
  .foot {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-2);
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .toggle {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    background: var(--hm-color-surface);
    color: var(--hm-color-text);
    font: inherit;
    font-weight: var(--hm-font-weight-medium);
  }
  .toggle:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .badge {
    min-inline-size: 20px;
    padding-inline: 6px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
    font-size: var(--hm-font-size-xs);
  }
</style>
