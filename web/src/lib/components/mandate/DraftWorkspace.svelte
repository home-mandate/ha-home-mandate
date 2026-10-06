<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The working area of the mandate and the template editor (design README 6.5–6.6): the
  settings card of the page, the approval defaults, the rules with their computed notes
  and the live preview, side by side on wide screens and as two tabs otherwise. Split from
  MandateEditor; the page owns loading, saving and which problems are shown, this part owns
  the editing of the rules (open form, moving, delete with undo, search) and the focus.
-->
<script lang="ts">
  import { tick, type Snippet } from 'svelte';
  import type { DeviceCatalog, MandateDraft, Rule } from '../../api/types.ts';
  import { overrides, ruleMatches } from '../../engine/analysis.ts';
  import { canonical } from '../../engine/vocabulary.ts';
  import { m } from '../../i18n.ts';
  import { appendRule, insertRule, moveRule, removeRule, replaceRule, withDefaults } from '../../mandate/edit.ts';
  import { decisionLabel } from '../../mandate/labels.ts';
  import { ruleNotes } from '../../mandate/notes.ts';
  import { isDefaultsProblem, type FieldProblem, type Part } from '../../mandate/problems.ts';
  import { isEditable } from '../../mandate/scope.ts';
  import { ruleLine, ruleText } from '../../mandate/text.ts';
  import { focusables } from '../../ui/focus.ts';
  import { Media, DESKTOP, WIDE } from '../../ui/media.svelte.ts';
  import { toasts } from '../../ui/toasts.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import TextField from '../TextField.svelte';
  import ApprovalFields from './ApprovalFields.svelte';
  import PreviewMatrix from './PreviewMatrix.svelte';
  import RuleCard from './RuleCard.svelte';
  import RuleForm from './RuleForm.svelte';

  interface Props {
    draft: MandateDraft;
    /** The stored draft the preview compares with. */
    previous: MandateDraft;
    /** Number of the stored version; null for a template, which has none. */
    version: number | null;
    catalog: DeviceCatalog;
    /** The device list could not be loaded: ids instead of names, no effects. */
    catalogMissing: boolean;
    readonly: boolean;
    /** People who can approve, by Home Assistant user id (or the placeholder of templates). */
    people: readonly { id: string; name: string }[];
    /** The agent, cleaned and isolated for use inside a sentence. */
    agent: string;
    locale: string;
    timeZone: string;
    /** Problems that may be shown now. */
    visible: readonly FieldProblem[];
    /** The draft has errors: nothing of it would apply. */
    invalid: boolean;
    /** Former IDs of renamed devices → current IDs. */
    renamed: ReadonlyMap<string, string>;
    /** Why the draft would not apply right now; "" if it would. */
    notInEffect?: string;
    /** Heading and content of the page's own settings card. */
    settingsTitle: string;
    settings: Snippet;
    onchange: (draft: MandateDraft) => void;
    /** A form was left: "rule:<id>" or "setting:<part>". */
    ontouch: (key: string) => void;
  }

  let {
    draft,
    previous,
    version,
    catalog,
    catalogMissing,
    readonly,
    people,
    agent,
    locale,
    timeZone,
    visible,
    invalid,
    renamed,
    notInEffect = '',
    settingsTitle,
    settings,
    onchange,
    ontouch,
  }: Props = $props();

  const uid = $props.id();
  /** From this many rules a search field appears above the list. */
  const SEARCH_FROM = 10;
  const TABS = ['rules', 'preview'] as const;
  type Tab = (typeof TABS)[number];
  const RULES: Tab = 'rules';
  const PREVIEW: Tab = 'preview';
  const TAB_LABELS: Record<Tab, () => string> = { rules: () => m.editor_tab_rules(), preview: () => m.editor_tab_preview() };

  const wide = new Media(WIDE);
  const desktop = new Media(DESKTOP);

  /** Id of the rule whose form is open. */
  let editing = $state<string | null>(null);
  let live = $state('');
  /** The last deleted rule, while nothing else was edited since: it can be put back. */
  let removed = $state.raw<{ rule: Rule; index: number; after: MandateDraft; toast: number } | null>(null);
  let search = $state('');
  let tab = $state<Tab>('rules');
  let dragged: number | null = null;

  let form: RuleForm | undefined = $state();
  let basics: HTMLElement | undefined = $state();
  let defaults: HTMLElement | undefined = $state();
  let adder: HTMLButtonElement | undefined = $state();
  const openers: Record<string, HTMLButtonElement | undefined> = $state({});
  const tabButtons: HTMLButtonElement[] = $state([]);

  const undoable = $derived(removed !== null && removed.after === draft ? removed : null);
  const ruleKey = (index: number) => `rule:${draft.rules[index]?.id ?? index}`;
  const texts = $derived(draft.rules.map((r) => ruleText(r, catalog, locale)));
  const matchCounts = $derived(ruleMatches(draft, catalog.devices));
  const overridden = $derived(overrides(draft, catalog.devices));
  const matchText = (index: number) => {
    const count = matchCounts[index] ?? 0;
    return count > 0 ? m.rule_count_matches({ count }) : m.rule_no_matches();
  };

  const shownRules = $derived.by(() => {
    const query = search.trim().toLowerCase();
    const all = draft.rules.map((rule, index) => ({ rule, index }));
    if (query === '' || draft.rules.length < SEARCH_FROM) return all;
    return all.filter(({ rule, index }) => {
      const text = texts[index];
      const words = text ? `${ruleLine(text)} ${decisionLabel(rule.decision)} ${text.conditions}` : '';
      return rule.id === editing || words.toLowerCase().includes(query);
    });
  });

  /** announce puts a message into the live region; emptied first so the same text is read again. */
  export async function announce(text: string) {
    live = '';
    await tick();
    live = text;
  }

  /** forget drops the undo of a delete: the edit it belonged to was replaced. */
  export function forget() {
    if (removed) toasts.dismiss(removed.toast);
    removed = null;
  }

  function changeRule(index: number, rule: Rule) {
    const before = draft.rules[index];
    onchange(replaceRule(draft, index, rule));
    // The confirmation was for another form of the rule (decision U9): say that it is gone.
    const lost = before?.allow_critical === true && rule.allow_critical !== true && rule.decision === 'allow';
    if (lost && canonical({ ...before, allow_critical: undefined }) !== canonical(rule)) void announce(m.critical_override_reset());
  }

  function move(from: number, to: number) {
    if (from === to) return;
    if (to < 0 || to >= draft.rules.length) {
      void announce(to < 0 ? m.rule_already_first({ n: from + 1 }) : m.rule_already_last({ n: from + 1 }));
      return;
    }
    onchange(moveRule(draft, from, to));
    void announce(m.rule_moved({ from: from + 1, to: to + 1, count: draft.rules.length }));
  }

  async function edit(ruleId: string) {
    editing = ruleId;
    tab = 'rules';
    await tick();
    form?.focusFirst();
  }

  async function done() {
    const ruleId = editing;
    editing = null;
    if (ruleId !== null) ontouch(`rule:${ruleId}`);
    await tick();
    if (ruleId !== null) openers[ruleId]?.focus();
  }

  async function add() {
    const next = appendRule(draft);
    onchange(next);
    ontouch('setting:rules');
    const added = next.rules.at(-1);
    if (added) await edit(added.id);
  }

  async function remove(index: number) {
    const rule = draft.rules[index];
    if (!rule) return;
    const after = removeRule(draft, index);
    onchange(after);
    if (editing === rule.id) editing = null;
    if (removed) toasts.dismiss(removed.toast);
    // The toast is the design's undo; the button under the list stays until the next edit,
    // so that the keyboard reaches it in time.
    const toast = toasts.show({
      kind: 'undo',
      text: m.rule_deleted({ n: index + 1 }),
      action: { label: m.common_undo(), run: () => void restore() },
    });
    removed = { rule, index, after, toast };
    void announce(m.rule_deleted({ n: index + 1 }));
    await tick();
    // The next rule that is shown (a search may hide some, an open form has no button), else "add rule".
    const next = shownRules.find((r) => r.index >= index && openers[r.rule.id]);
    (next ? openers[next.rule.id] : adder)?.focus();
  }

  /** restore undoes the last delete, as long as nothing else was edited since. */
  async function restore() {
    const undo = undoable;
    if (!undo) return;
    toasts.dismiss(undo.toast);
    removed = null;
    onchange(insertRule(draft, Math.min(undo.index, draft.rules.length), undo.rule));
    void announce(m.rule_restored({ n: undo.index + 1 }));
    await tick();
    (openers[undo.rule.id] ?? adder)?.focus();
  }

  /** show leads to the field of a problem: the rule's form, or the setting's first control. */
  export async function show(problem: FieldProblem) {
    tab = 'rules';
    if (problem.rule !== null) {
      const rule = draft.rules[problem.rule];
      if (rule && isEditable(rule)) await edit(rule.id);
      else if (rule) openers[rule.id]?.focus();
      return;
    }
    await tick();
    if (problem.part === 'rules') {
      adder?.focus();
      return;
    }
    const section = isDefaultsProblem(problem) ? defaults : basics;
    if (section) focusables(section)[0]?.focus();
  }

  const touchSetting = (part: Part) => ontouch(`setting:${part}`);

  function tabKey(event: KeyboardEvent) {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const index = (TABS.indexOf(tab) + 1) % TABS.length;
    tab = TABS[index] as Tab;
    tabButtons[index]?.focus();
  }
</script>

{#if !wide.matches}
  <div class="tabs" role="tablist">
    {#each TABS as t, i (t)}
      <button
        bind:this={tabButtons[i]}
        type="button"
        role="tab"
        id="{uid}-tab-{t}"
        aria-selected={tab === t}
        aria-controls="{uid}-panel-{t}"
        tabindex={tab === t ? 0 : -1}
        onclick={() => (tab = t)}
        onkeydown={tabKey}
      >
        {TAB_LABELS[t]()}
      </button>
    {/each}
  </div>
{/if}

<!-- Both panels stay mounted: the preview keeps its filter and selection when the tab changes. -->
<div class="columns" class:wide={wide.matches}>
  <div
    class="left"
    id="{uid}-panel-rules"
    role={wide.matches ? undefined : 'tabpanel'}
    aria-labelledby={wide.matches ? undefined : `${uid}-tab-rules`}
    hidden={!wide.matches && tab !== RULES}
  >
    <section bind:this={basics} class="card" aria-labelledby="{uid}-basics">
      <h2 id="{uid}-basics">{settingsTitle}</h2>
      {@render settings()}
    </section>

    <section bind:this={defaults} class="card" aria-labelledby="{uid}-defaults">
      <h2 id="{uid}-defaults">{m.editor_ask_defaults()}</h2>
      <ApprovalFields
        approval={draft.approval}
        {people}
        timeoutError={visible.find((p) => p.rule === null && p.part === 'timeout')?.text ?? ''}
        approversError={visible.find((p) => p.rule === null && p.part === 'approvers')?.text ?? ''}
        disabled={readonly}
        onchange={(approval) => onchange(withDefaults(draft, approval))}
        ontouch={touchSetting}
      />
    </section>

    <section class="rules" aria-labelledby="{uid}-rules">
      <div class="rules-head">
        <h2 id="{uid}-rules">{m.editor_rules()} <span class="n">({new Intl.NumberFormat(locale).format(draft.rules.length)})</span></h2>
        <p>{m.editor_rules_help()}</p>
      </div>
      {#if draft.rules.length >= SEARCH_FROM}
        <TextField type="search" label={m.rule_search()} bind:value={search} />
      {/if}
      <ol role="list">
        {#each shownRules as { rule, index } (rule.id)}
          {@const key = ruleKey(index)}
          {@const errors = visible.filter((p) => p.rule === index)}
          <RuleCard
            n={index + 1}
            count={draft.rules.length}
            text={texts[index] ?? ruleText(rule, catalog, locale)}
            decision={rule.decision}
            notes={ruleNotes(draft, index, catalog, overridden[index] ?? null, locale, !catalogMissing, renamed)}
            errors={errors.map((p) => p.text)}
            matches={matchText(index)}
            editing={editing === rule.id}
            editable={isEditable(rule)}
            {readonly}
            bind:opener={openers[rule.id]}
            onedit={() => void edit(rule.id)}
            onmove={(to) => move(index, to - 1)}
            onremove={() => void remove(index)}
            ondragstart={() => (dragged = index)}
            ondrop={() => {
              if (dragged !== null) move(dragged, index);
              dragged = null;
            }}
          >
            <RuleForm
              bind:this={form}
              {rule}
              {catalog}
              problems={errors}
              matches={matchText(index)}
              defaults={draft.approval}
              {people}
              {agent}
              {locale}
              {timeZone}
              onchange={(next) => changeRule(index, next)}
              ontouch={() => ontouch(key)}
              onremove={() => void remove(index)}
              ondone={() => void done()}
            />
          </RuleCard>
        {/each}
        {#if shownRules.length === 0 && draft.rules.length > 0}
          <li class="nothing">{m.rule_search_none()}</li>
        {/if}
        <li class="default">
          <Icon name="default" />
          <strong>{m.editor_default_rule()}</strong>
          <span>{m.editor_default_rule_desc()}</span>
          <Icon name="lock" />
        </li>
      </ol>
      <span class="hm-visually-hidden" aria-live="polite">{live}</span>
      {#if !readonly}
        <div class="add">
          <Button bind:element={adder} size="lg" icon="plus" onclick={() => void add()}>{m.editor_add_rule()}</Button>
          {#if undoable}
            <Button size="lg" onclick={() => void restore()}>{m.rule_undo_delete({ n: undoable.index + 1 })}</Button>
          {/if}
          <span>{m.editor_add_rule_hint()}</span>
        </div>
      {/if}
    </section>
  </div>

  <div
    class="right"
    id="{uid}-panel-preview"
    role={wide.matches ? undefined : 'tabpanel'}
    aria-labelledby={wide.matches ? undefined : `${uid}-tab-preview`}
    hidden={!wide.matches && tab !== PREVIEW}
  >
    <PreviewMatrix {draft} {previous} {version} {catalog} {locale} grid={desktop.matches} {invalid} {catalogMissing} {notInEffect} />
  </div>
</div>

<style>
  .tabs {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--hm-space-1);
    padding: var(--hm-space-1);
    border-radius: 10px;
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border);
  }
  .tabs button {
    min-block-size: var(--hm-size-touch);
    border-radius: 7px;
    border: none;
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text-muted);
    background: transparent;
    cursor: pointer;
  }
  .tabs button[aria-selected='true'] {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    box-shadow: var(--hm-shadow-sm);
  }
  .columns {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--hm-space-6);
    align-items: start;
  }
  .columns.wide {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
  .wide .right {
    position: sticky;
    inset-block-start: var(--hm-space-4);
    max-block-size: calc(100dvh - 2 * var(--hm-space-4));
    overflow-y: auto;
    border-radius: var(--hm-radius-lg);
  }
  .left {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-5);
    min-inline-size: 0;
  }
  .right {
    min-inline-size: 0;
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding: 18px var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  h2 {
    margin: 0;
    font-size: 17px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .rules {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .rules-head {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
  }
  .rules-head h2 {
    font-size: 20px;
  }
  .n {
    font-weight: var(--hm-font-weight-regular);
    color: var(--hm-color-text-subtle);
  }
  .rules-head p {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  ol {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .default {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
    padding: 14px var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    border: var(--hm-border-width) dashed var(--hm-color-default-border);
    background: var(--hm-color-default-bg);
    color: var(--hm-color-default-fg);
    font-size: 15px;
  }
  .default strong {
    font-weight: var(--hm-font-weight-semibold);
  }
  .default span {
    flex: 1 1 140px;
  }
  .nothing {
    padding: var(--hm-space-3) var(--hm-space-4);
    font-size: 15px;
    color: var(--hm-color-text-muted);
  }
  .add {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  [hidden] {
    display: none;
  }
</style>
