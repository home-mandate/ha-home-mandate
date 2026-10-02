<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The form of one rule (design README 6.5): device scope, actions, decision, the separate
  confirmation for critical actions, approval settings and conditions. Every edit goes to
  the parent as a new rule; the form keeps no copy of its own.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import type { Approval, DeviceCatalog, Rule, Weekday } from '../../api/types.ts';
  import { CATEGORIES, isCritical } from '../../engine/vocabulary.ts';
  import { m } from '../../i18n.ts';
  import {
    toggleAction,
    toggleAllActions,
    toggleWeekday,
    withAllowCritical,
    withApproval,
    withArea,
    withCategory,
    withDecision,
    withDevice,
    withWindow,
  } from '../../mandate/edit.ts';
  import { actionLabel, categoryLabel, WEEKDAYS, weekdayNames } from '../../mandate/labels.ts';
  import { criticalDevices, demotedNames, includedNames } from '../../mandate/notes.ts';
  import type { FieldProblem, Part } from '../../mandate/problems.ts';
  import { categoryOf, deviceOptions, scopeOf, vocabularyOf, type ScopeCategory } from '../../mandate/scope.ts';
  import { windowText } from '../../mandate/text.ts';
  import { timeoutText } from '../../mandate/timeout.ts';
  import { focusables } from '../../ui/focus.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import DecisionSegment from '../DecisionSegment.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import ToggleChip from '../ToggleChip.svelte';
  import ApprovalFields from './ApprovalFields.svelte';
  import CriticalOverride from './CriticalOverride.svelte';

  interface Props {
    rule: Rule;
    catalog: DeviceCatalog;
    /** Problems of this rule that may be shown now. */
    problems: readonly FieldProblem[];
    /** "Matches 12 devices". */
    matches: string;
    /** The mandate's approval settings, which an "ask" rule uses unless it has its own. */
    defaults: Approval;
    people: readonly { id: string; name: string }[];
    /** Name of the agent, cleaned and isolated for use inside a sentence. */
    agent: string;
    locale: string;
    timeZone: string;
    onchange: (rule: Rule) => void;
    ontouch: () => void;
    onremove: () => void;
    ondone: () => void;
  }

  let { rule, catalog, problems, matches, defaults, people, agent, locale, timeZone, onchange, ontouch, onremove, ondone }: Props = $props();

  const id = $props.id();
  const DEFAULT_WINDOW = '22:00-06:00';

  let root: HTMLElement | undefined = $state();

  /** focusFirst moves the focus into the form when it opens. */
  export function focusFirst() {
    if (root) focusables(root)[0]?.focus();
  }

  const devices = $derived(catalog.devices);
  const scope = $derived(scopeOf(rule, devices));
  const category = $derived(categoryOf(rule, devices));
  const vocabulary = $derived(vocabularyOf(rule, devices));
  const all = $derived(rule.actions.includes('*'));
  const extra = $derived(rule.actions.filter((a) => a !== '*' && !vocabulary.includes(a)));
  const misfits = $derived(new Set(problems.filter((p) => p.action !== null).map((p) => p.action)));
  const problem = (part: Part) => problems.find((p) => p.part === part && p.action === null)?.text ?? '';
  const scopeError = $derived(problem('scope'));
  const decisionError = $derived(problem('decision'));
  const windowError = $derived(problem('window'));
  const weekdaysError = $derived(problem('weekdays'));
  const timeoutError = $derived(problem('timeout'));
  const approversError = $derived(problem('approvers'));

  const categoryOptions = $derived([
    { value: 'all', label: m.rule_all_devices() },
    ...CATEGORIES.map((c) => ({ value: c as string, label: categoryLabel(c) })),
  ]);
  const areaOptions = $derived.by(() => {
    const options = catalog.areas.map((a) => ({ value: a.id, label: cleanUntrusted(a.name) || cleanUntrusted(a.id) }));
    const missing = scope.area !== null && !catalog.areas.some((a) => a.id === scope.area);
    return [{ value: '', label: m.rule_any() }, ...options, ...(missing && scope.area !== null ? [{ value: scope.area, label: cleanUntrusted(scope.area) }] : [])];
  });
  const deviceChoices = $derived.by(() => {
    const options = deviceOptions(scope, devices).map((d) => ({ value: d.entity_id, label: cleanUntrusted(d.name) || cleanUntrusted(d.entity_id) }));
    const missing = scope.device !== null && !options.some((o) => o.value === scope.device);
    return [{ value: '', label: m.rule_any() }, ...options, ...(missing && scope.device !== null ? [{ value: scope.device, label: cleanUntrusted(scope.device) }] : [])];
  });

  const included = $derived(includedNames(rule, devices, locale));
  const demotable = $derived(rule.decision === 'allow' ? demotedNames(rule, devices, locale) : '');
  const consequence = $derived(
    m.critical_confirm_body({
      agent,
      actions: demotable,
      devices: m.preview_device_count({ count: criticalDevices(rule, devices).length }),
    }),
  );

  const timeWindow = $derived(rule.conditions?.time_window);
  const [from = '', to = ''] = $derived(timeWindow?.split('-') ?? []);
  const actionProblems = $derived(problems.filter((p) => p.part === 'actions'));
  const days = $derived(rule.conditions?.weekdays ?? WEEKDAYS);
  const dayNames = $derived(weekdayNames(locale));

  function change(next: Rule, touch = true) {
    onchange(next);
    if (touch) ontouch();
  }

  const setWindow = (start: string, end: string) => change(withWindow(rule, `${start}-${end}`), false);

  /** Errors show once the focus leaves the form, not on the way from one of its fields to the next. */
  function left(event: FocusEvent) {
    if (!(event.relatedTarget instanceof Node) || !root?.contains(event.relatedTarget)) ontouch();
  }

  /** Own approval settings on or off: the button that did it is gone, so the focus moves to what replaced it. */
  async function ownApproval(approval: Approval | null) {
    change(withApproval(rule, approval));
    await tick();
    root?.querySelector<HTMLElement>(approval ? '.own input' : '.inherit button')?.focus();
  }
</script>

<div bind:this={root} class="form" onfocusout={left}>
  <fieldset class="scope">
    <legend>{m.rule_devices()}</legend>
    <div class="selects">
      <SelectField label={m.rule_category()} value={scope.category} options={categoryOptions} onchange={(v) => change(withCategory(rule, v as ScopeCategory, devices))} />
      <SelectField label={m.rule_area()} value={scope.area ?? ''} options={areaOptions} onchange={(v) => change(withArea(rule, v || null, devices))} />
      <SelectField label={m.rule_device()} value={scope.device ?? ''} options={deviceChoices} onchange={(v) => change(withDevice(rule, v || null, devices))} />
    </div>
    {#if scopeError}<span class="note danger"><Icon name="warning" size={16} />{scopeError}</span>{/if}
  </fieldset>

  <fieldset aria-describedby={actionProblems.length > 0 ? `${id}-misfits` : undefined}>
    <legend>{m.rule_actions()}</legend>
    <div class="chips">
      <ToggleChip pressed={all} onchange={() => change(toggleAllActions(rule))}>{m.rule_all_actions()}</ToggleChip>
      {#each vocabulary as action (action)}
        <ToggleChip
          pressed={all || rule.actions.includes(action)}
          critical={category !== undefined && isCritical(category, action)}
          onchange={() => change(toggleAction(rule, action, vocabulary))}
        >
          {actionLabel(category, action)}
        </ToggleChip>
      {/each}
      {#each extra as action (action)}
        {#if misfits.has(action)}
          <button type="button" class="misfit" aria-pressed="true" aria-describedby="{id}-misfits" onclick={() => change(toggleAction(rule, action, vocabulary))}>
            <Icon name="warning" size={16} /><span>{actionLabel(category, action)}</span>
          </button>
        {:else}
          <ToggleChip pressed onchange={() => change(toggleAction(rule, action, vocabulary))}>{actionLabel(category, action)}</ToggleChip>
        {/if}
      {/each}
    </div>
    {#if actionProblems.length > 0}
      <div id="{id}-misfits" class="notes">
        {#each actionProblems as p (p.text)}
          <span class="note danger boxed"><Icon name="warning" size={16} /><span>{p.text}</span></span>
        {/each}
      </div>
    {/if}
    {#if included}
      <span class="note critical boxed"><Icon name="critical" size={16} /><span>{m.rule_all_includes_critical({ actions: included })}</span></span>
    {/if}
    {#if !all && rule.actions.length > 0 && !rule.actions.includes('read')}
      <span class="note"><Icon name="info" size={16} /><span>{m.rule_read_not_included()}</span></span>
    {/if}
  </fieldset>

  <div class="decision">
    <DecisionSegment value={rule.decision} onchange={(decision) => change(withDecision(rule, decision))} />
    {#if decisionError}<span class="note danger"><Icon name="warning" size={16} />{decisionError}</span>{/if}
  </div>

  {#if demotable}
    <CriticalOverride
      on={rule.allow_critical === true}
      {consequence}
      onconfirm={() => change(withAllowCritical(rule, true))}
      onoff={() => change(withAllowCritical(rule, false))}
    />
  {/if}

  {#if rule.decision === 'ask'}
    {#if rule.approval}
      <div class="own">
        <ApprovalFields
          approval={rule.approval}
          {people}
          timeoutError={timeoutError}
          approversError={approversError}
          onchange={(approval) => change(withApproval(rule, approval), false)}
          ontouch={() => ontouch()}
        />
        <Button variant="text" size="lg" onclick={() => void ownApproval(null)}>{m.rule_ask_default()}</Button>
      </div>
    {:else}
      <div class="inherit">
        <span>{m.rule_ask_inherit({ timeout: timeoutText(defaults.timeout, locale) })}</span>
        <Button variant="text" size="lg" icon="plus" onclick={() => void ownApproval({ timeout: defaults.timeout, approvers: [...defaults.approvers] })}>
          {m.rule_ask_custom()}
        </Button>
      </div>
    {/if}
  {/if}

  <fieldset>
    <legend>{m.rule_conditions()}</legend>
    <div class="window">
      <label class="check">
        <input type="checkbox" checked={timeWindow !== undefined} onchange={(e) => change(withWindow(rule, e.currentTarget.checked ? DEFAULT_WINDOW : null))} />
        {m.time_window_label()}
      </label>
      {#if timeWindow !== undefined}
        <label class="time">
          {m.time_from()}
          <input type="time" value={from} aria-invalid={windowError ? 'true' : undefined} aria-describedby="{id}-window" oninput={(e) => setWindow(e.currentTarget.value, to)} />
        </label>
        <label class="time">
          {m.time_to()}
          <input type="time" value={to} aria-invalid={windowError ? 'true' : undefined} aria-describedby="{id}-window" oninput={(e) => setWindow(from, e.currentTarget.value)} />
        </label>
      {/if}
    </div>
    {#if timeWindow !== undefined}
      <span id="{id}-window" class="note strong" class:danger={windowError}>
        {#if windowError}
          <Icon name="warning" size={16} /><span>{windowError}</span>
        {:else}
          <Icon name="history" size={16} /><span>{windowText(timeWindow, locale)} · {m.common_timezone_note({ tz: timeZone })}</span>
        {/if}
      </span>
    {/if}
    <div class="chips" role="group" aria-label={m.weekdays_label()} aria-describedby="{id}-days">
      {#each WEEKDAYS as day (day)}
        <ToggleChip pressed={days.includes(day)} onchange={() => change(toggleWeekday(rule, day as Weekday))}>{dayNames[day]}</ToggleChip>
      {/each}
    </div>
    <span id="{id}-days" class="note" class:danger={weekdaysError}>
      {#if weekdaysError}
        <Icon name="warning" size={16} /><span>{weekdaysError}</span>
      {:else if rule.conditions?.weekdays === undefined}
        {m.weekdays_all()}
      {/if}
    </span>
  </fieldset>

  <div class="foot">
    <span class="matches">{matches}</span>
    <Button variant="text" size="lg" class="remove" onclick={onremove}>{m.common_delete()}</Button>
    <Button variant="primary" size="lg" onclick={ondone}>{m.rule_done()}</Button>
  </div>
</div>

<style>
  .form {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    padding: var(--hm-space-4) var(--hm-space-5) var(--hm-space-5);
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    min-inline-size: 0;
    margin: 0;
    padding: 0;
    border: none;
  }
  legend {
    padding: 0 0 6px;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .selects {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr));
    gap: var(--hm-space-3);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .chips :global(.chip) {
    min-block-size: var(--hm-size-touch);
  }
  .misfit {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-pill);
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-danger-bg);
    border: 2px solid var(--hm-color-danger-fg);
    cursor: pointer;
  }
  .misfit span {
    text-decoration: line-through;
  }
  .notes {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .note {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .note :global(svg) {
    flex-shrink: 0;
    margin-block-start: 2px;
  }
  .note.strong {
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text);
  }
  .note.danger {
    color: var(--hm-color-danger-fg);
  }
  .note.critical {
    color: var(--hm-color-critical-fg);
  }
  .boxed {
    padding: var(--hm-space-2) 10px;
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid;
  }
  .danger.boxed {
    background: var(--hm-color-danger-bg);
    border-color: var(--hm-color-danger-border);
  }
  .critical.boxed {
    background: var(--hm-color-critical-bg);
    border-color: var(--hm-color-critical-border);
  }
  .decision {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .own {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-2);
    padding: 14px;
    border-radius: 10px;
    border: var(--hm-border-width) solid var(--hm-color-border);
  }
  .own :global(.fields) {
    align-self: stretch;
  }
  .inherit {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px var(--hm-space-3);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .window {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: var(--hm-space-2) var(--hm-space-3);
  }
  .check {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    font-size: var(--hm-font-size-sm);
    cursor: pointer;
  }
  .check input {
    inline-size: 20px;
    block-size: 20px;
    margin: 0;
    accent-color: var(--hm-color-accent);
  }
  .time {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .time input {
    min-block-size: var(--hm-size-touch);
    padding-block: 0;
    padding-inline: var(--hm-space-2);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  .time input[aria-invalid='true'] {
    border-color: var(--hm-color-danger-fg);
    outline: 1px solid var(--hm-color-danger-fg);
    outline-offset: 0;
  }
  .foot {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
    padding-block-start: var(--hm-space-3);
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .matches {
    flex: 1 1 160px;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .foot :global(.remove) {
    color: var(--hm-color-danger-fg);
  }
  .foot :global(.remove:hover) {
    background: var(--hm-color-danger-bg);
  }
</style>
