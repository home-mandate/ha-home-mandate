<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Mandate editor (design README 6.5–6.6). It edits a local draft of the stored version and
  shows live what the draft would mean: computed notes per rule and the preview matrix,
  both from the UI's own evaluation. Saving goes through a summary and never overwrites
  silently: if someone else stored a version meanwhile, the editor says so and keeps the
  edit. The server checks every version again and decides every real request itself.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { ApiError } from '../api/client.ts';
  import type { ApproverList, DeviceCatalog, MandateDetail, MandateDraft, Rule } from '../api/types.ts';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import TextField from '../components/TextField.svelte';
  import ApprovalFields from '../components/mandate/ApprovalFields.svelte';
  import BasicsForm from '../components/mandate/BasicsForm.svelte';
  import MandateStatus from '../components/mandate/MandateStatus.svelte';
  import PreviewMatrix from '../components/mandate/PreviewMatrix.svelte';
  import RuleCard from '../components/mandate/RuleCard.svelte';
  import RuleForm from '../components/mandate/RuleForm.svelte';
  import SaveDialog from '../components/mandate/SaveDialog.svelte';
  import VersionChip from '../components/mandate/VersionChip.svelte';
  import { overrides, ruleMatches } from '../engine/analysis.ts';
  import { canonical, needsCriticalConfirmation } from '../engine/vocabulary.ts';
  import { m } from '../i18n.ts';
  import { countChanges, type Edited } from '../mandate/changes.ts';
  import { effectiveStatus, type EffectiveStatus } from '../mandate/dates.ts';
  import { appendRule, insertRule, moveRule, removeRule, replaceRule, withDefaults, withoutRevokedConfirmations } from '../mandate/edit.ts';
  import { decisionLabel } from '../mandate/labels.ts';
  import { ruleNotes } from '../mandate/notes.ts';
  import { describeProblems, type FieldProblem, type Part } from '../mandate/problems.ts';
  import { isEditable } from '../mandate/scope.ts';
  import { ruleLine, ruleText } from '../mandate/text.ts';
  import { currentNumber, draftOf } from '../mandate/versions.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { focusables } from '../ui/focus.ts';
  import { Media, DESKTOP, WIDE } from '../ui/media.svelte.ts';
  import { toasts } from '../ui/toasts.ts';
  import { cleanUntrusted, isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
    id: string;
    /** Browser clock, ticking; the status follows the server's time. */
    now: number;
  }

  interface Data {
    detail: MandateDetail;
    /** null: the device list could not be loaded. */
    catalog: DeviceCatalog | null;
    approvers: ApproverList;
  }

  let { app, id, now }: Props = $props();

  const uid = $props.id();
  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const NO_APPROVERS: ApproverList = { approvers: [], candidates: { people: [], notify_services: [] } };
  /** From this many rules a search field appears above the list. */
  const SEARCH_FROM = 10;
  const NOT_IN_EFFECT: Record<EffectiveStatus, () => string> = {
    active: () => '',
    not_yet_valid: () => m.status_not_yet_valid(),
    expired: () => m.status_expired(),
    revoked: () => m.status_revoked(),
  };
  const TABS = ['rules', 'preview'] as const;
  type Tab = (typeof TABS)[number];
  const RULES: Tab = 'rules';
  const PREVIEW: Tab = 'preview';
  const NAME_PART: Part = 'name';
  const TAB_LABELS: Record<Tab, () => string> = { rules: () => m.editor_tab_rules(), preview: () => m.editor_tab_preview() };

  const wide = new Media(WIDE);
  const desktop = new Media(DESKTOP);
  const page = new Loader<Data>(async () => {
    const api = app.api;
    const [detail, catalog, approvers] = await Promise.all([
      api.mandate(id),
      // Without Home Assistant the editor shows ids instead of names; rules stay editable.
      api.devices().catch(() => null),
      api.approvers().catch(() => NO_APPROVERS),
    ]);
    return { detail, catalog, approvers };
  });

  /** The version the edit is based on. */
  let stored = $state.raw<MandateDetail | null>(null);
  /** A newer version on the server while this edit has unsaved changes. */
  let newer = $state.raw<MandateDetail | null>(null);
  let name = $state('');
  let draft = $state.raw<MandateDraft | null>(null);
  /** Id of the rule whose form is open. */
  let editing = $state<string | null>(null);
  let attempted = $state(false);
  const touched = new SvelteSet<string>();
  let saveOpen = $state(false);
  let saving = $state(false);
  let saveError = $state('');
  let live = $state('');
  /** A reload asked for while a save was running; it runs afterwards. */
  let reloadPending = false;
  /** The last deleted rule, while nothing else was edited since: it can be put back. */
  let removed = $state.raw<{ rule: Rule; index: number; after: MandateDraft; toast: number } | null>(null);
  let search = $state('');
  let tab = $state<Tab>('rules');
  let dragged: number | null = null;

  let form: RuleForm | undefined = $state();
  let summary: HTMLElement | undefined = $state();
  let basics: HTMLElement | undefined = $state();
  let defaults: HTMLElement | undefined = $state();
  let adder: HTMLButtonElement | undefined = $state();
  const openers: Record<string, HTMLButtonElement | undefined> = $state({});
  const tabButtons: HTMLButtonElement[] = $state([]);

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const catalog = $derived(page.data?.catalog ?? NO_CATALOG);
  const catalogMissing = $derived(page.data !== null && page.data.catalog === null);
  const base: Edited | null = $derived(stored ? { name: stored.summary.name, draft: draftOf(stored.document) } : null);
  const edited: Edited | null = $derived(draft ? { name, draft } : null);
  const changes = $derived(base && edited ? countChanges(base, edited) : 0);
  const readonly = $derived(stored?.summary.status === 'revoked');
  const version = $derived(currentNumber(stored?.versions ?? []));
  const agent = $derived(isolate(stored?.document.agent.display_name));
  const serverNow = $derived(now - app.offsetMs);
  /** Whether the draft, once saved, would apply right now. */
  const notInEffect = $derived(
    stored && draft ? NOT_IN_EFFECT[effectiveStatus({ status: stored.summary.status, valid_from: draft.valid_from, expires: draft.expires }, serverNow)]() : '',
  );
  const undoable = $derived(removed !== null && removed.after === draft ? removed : null);

  const problems = $derived(draft ? describeProblems(name, draft) : []);
  const ruleKey = (index: number) => `rule:${draft?.rules[index]?.id ?? index}`;
  /** What must have been left for a problem to show: the rule's form, or the field of a setting. */
  const fieldOf = (p: FieldProblem) => (p.rule !== null ? ruleKey(p.rule) : `setting:${p.part}`);
  const isDefault = (p: FieldProblem) => p.rule === null && (p.part === 'timeout' || p.part === 'approvers');
  const isBasic = (p: FieldProblem) => p.rule === null && !isDefault(p) && p.part !== 'rules';
  const visible = $derived(problems.filter((p) => attempted || touched.has(fieldOf(p))));
  const storedInvalid = $derived(base ? describeProblems(base.name, base.draft).some((p) => p.part !== 'name') : false);

  const texts = $derived(draft ? draft.rules.map((r) => ruleText(r, catalog, ctx.locale)) : []);
  const matchCounts = $derived(draft ? ruleMatches(draft, catalog.devices) : []);
  const overridden = $derived(draft ? overrides(draft, catalog.devices) : []);
  const matchText = (index: number) => {
    const count = matchCounts[index] ?? 0;
    return count > 0 ? m.rule_count_matches({ count }) : m.rule_no_matches();
  };

  /** People for the approver chips: those set up, and whoever a version still names. */
  const people = $derived.by(() => {
    const approvers = page.data?.approvers ?? NO_APPROVERS;
    const names = new Map([...approvers.candidates.people, ...approvers.approvers].map((p) => [p.user_id, p.name]));
    const named = [draft, base?.draft].flatMap((d) => (d ? [...d.approval.approvers, ...d.rules.flatMap((r) => r.approval?.approvers ?? [])] : []));
    const ids = [...new Set([...approvers.approvers.map((a) => a.user_id), ...named])];
    return ids.map((user) => ({ id: user, name: names.get(user) ?? user }));
  });
  const peopleNames = $derived(new Map(people.map((p) => [p.id, p.name])));

  const shownRules = $derived.by(() => {
    const query = search.trim().toLowerCase();
    if (!draft) return [];
    const all = draft.rules.map((rule, index) => ({ rule, index }));
    if (query === '' || draft.rules.length < SEARCH_FROM) return all;
    return all.filter(({ rule, index }) => {
      const text = texts[index];
      const words = text ? `${ruleLine(text)} ${decisionLabel(rule.decision)} ${text.conditions}` : '';
      return rule.id === editing || words.toLowerCase().includes(query);
    });
  });

  // An unsaved edit survives leaving the page (versions, audit log) while the app is open.
  $effect(() => {
    if (stored && draft && changes > 0) app.unsaved.set(id, { stored, name, draft });
    else if (stored) app.unsaved.delete(id);
  });

  function adopt(detail: MandateDetail) {
    // An undo belongs to the edit that is replaced here; it must not reach into the new one.
    if (removed) toasts.dismiss(removed.toast);
    removed = null;
    stored = detail;
    newer = null;
    name = detail.summary.name;
    draft = draftOf(detail.document);
    attempted = false;
    touched.clear();
  }

  /** incoming handles a version loaded from the server, at first and while the editor is open. */
  function incoming(detail: MandateDetail) {
    let current = stored;
    if (!current) {
      // First load: pick up an edit left behind earlier, if there is one.
      const kept = app.unsaved.get(id);
      if (!kept) {
        adopt(detail);
        return;
      }
      current = stored = kept.stored;
      name = kept.name;
      draft = kept.draft;
    }
    if (detail.summary.digest === current.summary.digest) {
      // The same version; name or status may have changed without a new one. Keeping the
      // stored object otherwise keeps the evaluation's cache for it.
      const same = detail.summary.name === current.summary.name && detail.summary.status === current.summary.status;
      const untouched = name === current.summary.name;
      if (!same) stored = detail;
      if (untouched) name = detail.summary.name;
      newer = null;
      return;
    }
    if (changes > 0) {
      newer = detail;
      return;
    }
    adopt(detail);
    toasts.show({ kind: 'success', text: m.editor_updated_toast({ version: currentNumber(detail.versions) }) });
  }

  async function reload() {
    if (saving) {
      reloadPending = true;
      return;
    }
    await page.run();
    if (page.data && !saving) incoming(page.data.detail);
  }

  // Someone else's version stays a conflict only while there is something to lose.
  $effect(() => {
    if (newer && changes === 0) adopt(newer);
  });

  /** announce puts a message into the live region; emptied first so the same text is read again. */
  async function announce(text: string) {
    live = '';
    await tick();
    live = text;
  }

  onMount(() => {
    const stop = [
      app.on('mandates.changed', (event) => {
        if (event.id === id) void reload();
      }),
      app.on('approvers.changed', () => void reload()),
      app.on('reconnected', () => void reload()),
    ];
    void reload();
    return () => stop.forEach((off) => off());
  });

  // ---------------------------------------------------------------------------
  // Edits

  function touch(section: string) {
    touched.add(section);
  }

  function changeRule(index: number, rule: Rule) {
    if (!draft) return;
    const before = draft.rules[index];
    draft = replaceRule(draft, index, rule);
    // The confirmation was for another form of the rule (decision U9): say that it is gone.
    const lost = before?.allow_critical === true && rule.allow_critical !== true && rule.decision === 'allow';
    if (lost && canonical({ ...before, allow_critical: undefined }) !== canonical(rule)) void announce(m.critical_override_reset());
  }

  function move(from: number, to: number) {
    if (!draft || from === to) return;
    if (to < 0 || to >= draft.rules.length) {
      void announce(to < 0 ? m.rule_already_first({ n: from + 1 }) : m.rule_already_last({ n: from + 1 }));
      return;
    }
    draft = moveRule(draft, from, to);
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
    if (ruleId !== null) touch(`rule:${ruleId}`);
    await tick();
    if (ruleId !== null) openers[ruleId]?.focus();
  }

  async function add() {
    if (!draft) return;
    draft = appendRule(draft);
    touch('setting:rules');
    const added = draft.rules.at(-1);
    if (added) await edit(added.id);
  }

  async function remove(index: number) {
    if (!draft) return;
    const rule = draft.rules[index];
    if (!rule) return;
    const after = removeRule(draft, index);
    draft = after;
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
    if (!undo || !draft) return;
    toasts.dismiss(undo.toast);
    removed = null;
    draft = insertRule(draft, Math.min(undo.index, draft.rules.length), undo.rule);
    void announce(m.rule_restored({ n: undo.index + 1 }));
    await tick();
    (openers[undo.rule.id] ?? adder)?.focus();
  }

  // ---------------------------------------------------------------------------
  // Saving

  async function trySave() {
    if (readonly || !draft || saveOpen) return;
    if (problems.length > 0) {
      attempted = true;
      await tick();
      summary?.focus();
      return;
    }
    if (changes === 0) return;
    saveError = '';
    saveOpen = true;
  }

  async function save() {
    if (!stored || !base || !draft || saving || readonly) return;
    saving = true;
    saveError = '';
    let conflict = false;
    try {
      const detail = await app.api.putMandate(id, {
        name: name.trim(),
        draft,
        base_digest: stored.summary.digest,
        ...(needsCriticalConfirmation(base.draft, draft) ? { confirm_critical: true } : {}),
      });
      adopt(detail);
      page.set({ detail, catalog, approvers: page.data?.approvers ?? NO_APPROVERS });
      saveOpen = false;
      toasts.show({ kind: 'success', text: m.toast_saved({ version: currentNumber(detail.versions) }) });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'internal';
      conflict = code === 'conflict';
      const rejected = code === 'invalid_mandate' || code === 'invalid_input' || code === 'critical_confirmation_required';
      if (!conflict) saveError = rejected ? m.save_rejected() : m.toast_save_failed();
    } finally {
      saving = false;
    }
    if (conflict) {
      saveOpen = false;
      reloadPending = false;
      await reload();
      // The banner explains a newer version. If the reload brought nothing (it failed, or the
      // mandate was revoked), the save must not end without a word.
      if (!newer) toasts.show({ kind: 'error', text: m.toast_save_failed() });
    } else if (reloadPending) {
      reloadPending = false;
      await reload();
    }
  }

  /**
   * Keeps the edit and puts it on top of the newer version; the summary then shows the
   * difference to that version. A confirmation for critical actions that the newer version
   * took back does not come along: it has to be given again.
   */
  function rebase() {
    if (!newer || !base || !draft) return;
    const result = withoutRevokedConfirmations(draft, base.draft, draftOf(newer.document));
    draft = result.draft;
    stored = newer;
    newer = null;
    if (result.dropped) void announce(m.critical_override_reset());
  }

  function keydown(event: KeyboardEvent) {
    if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 's') return;
    event.preventDefault();
    void trySave();
  }

  function beforeunload(event: BeforeUnloadEvent) {
    if (changes > 0) event.preventDefault();
  }

  async function show(problem: FieldProblem) {
    tab = 'rules';
    if (problem.rule !== null) {
      const rule = draft?.rules[problem.rule];
      if (rule && isEditable(rule)) await edit(rule.id);
      else if (rule) openers[rule.id]?.focus();
      return;
    }
    await tick();
    if (problem.part === 'rules') {
      adder?.focus();
      return;
    }
    const section = isDefault(problem) ? defaults : basics;
    if (section) focusables(section)[0]?.focus();
  }

  const touchSetting = (part: Part) => touch(`setting:${part}`);

  function tabKey(event: KeyboardEvent) {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    const index = (TABS.indexOf(tab) + 1) % TABS.length;
    tab = TABS[index] as Tab;
    tabButtons[index]?.focus();
  }

  const problemText = (p: FieldProblem) => (p.rule === null ? p.text : m.validation_in_rule({ rule: m.rule_ref({ n: p.rule + 1 }), problem: p.text }));
</script>

<svelte:window onkeydown={keydown} onbeforeunload={beforeunload} />

{#if page.status === 'error' && page.code === 'not_found'}
  <EmptyState icon="search" title={m.editor_not_found_title()} body={m.editor_not_found_body()}>
    {#snippet action()}<a class="back" href={href({ name: 'mandates' })}><Icon name="back" size={16} />{m.editor_back()}</a>{/snippet}
  </EmptyState>
{:else if page.status === 'error'}
  <ErrorState title={m.editor_error_title()} body={m.mandates_error_body()} onretry={() => void reload()} />
{:else if !stored || !draft || !base || !edited}
  <div role="status" aria-busy="true" aria-label={m.common_loading()}><Skeleton lines={['30%', '70%', '50%']} /></div>
{:else}
  <div class="head">
    <a class="back" href={href({ name: 'mandates' })}><Icon name="back" size={16} />{m.editor_back()}</a>
    <div class="bar">
      <div class="title">
        <h1><bdi>{cleanUntrusted(name) || cleanUntrusted(stored.summary.name)}</bdi></h1>
        <MandateStatus status={effectiveStatus({ status: stored.summary.status, valid_from: base.draft.valid_from, expires: base.draft.expires }, serverNow)} />
        <VersionChip {version} digest={stored.summary.digest} />
      </div>
      <div class="actions">
        {#if !readonly}
          <span class="state" class:dirty={changes > 0}>
            <span class="mark" aria-hidden="true"></span>{changes > 0 ? m.editor_unsaved({ count: changes }) : m.editor_saved_state()}
          </span>
          <!-- Announced when the state flips, not with every change of the count. -->
          <span class="hm-visually-hidden" role="status">{changes > 0 ? m.editor_unsaved_any() : m.editor_saved_state()}</span>
        {/if}
        <a class="versions" href={href({ name: 'mandate_versions', id })}><Icon name="history" />{m.editor_versions()}</a>
        {#if !readonly}
          <Button
            variant="primary"
            size="lg"
            disabled={changes === 0 && problems.length === 0}
            aria-describedby={visible.length > 0 ? `${uid}-problems` : undefined}
            aria-keyshortcuts="Control+S Meta+S"
            onclick={() => void trySave()}
          >
            {m.editor_save()}
          </Button>
        {/if}
      </div>
    </div>
    {#if visible.length > 0}
      <div bind:this={summary} id="{uid}-problems" class="problems" role="group" aria-labelledby="{uid}-problem-count" tabindex="-1">
        <span id="{uid}-problem-count" class="count" role="alert"><Icon name="warning" />{m.validation_summary({ count: visible.length })}</span>
        <ul role="list">
          {#each visible as p (`${p.rule}/${p.part}/${p.text}`)}
            <li><button type="button" onclick={() => void show(p)}>{problemText(p)}</button></li>
          {/each}
        </ul>
      </div>
    {/if}
  </div>

  {#if readonly}
    <Banner kind="info" body={m.editor_revoked_note()} />
  {:else if storedInvalid}
    <Banner kind="critical" body={m.code_invalid_mandate()} />
  {/if}
  {#if newer}
    <div class="conflict" role="alert">
      <span class="lead"><Icon name="warning" /><strong>{m.editor_conflict_title()}</strong></span>
      <span>{m.editor_conflict_body({ version: currentNumber(newer.versions) })}</span>
      <div class="choices">
        <Button size="lg" onclick={rebase}>{m.editor_conflict_keep({ version: currentNumber(newer.versions) })}</Button>
        <Button size="lg" onclick={() => newer && adopt(newer)}>{m.editor_conflict_discard()}</Button>
      </div>
    </div>
  {/if}

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
          <h2 id="{uid}-basics">{m.editor_basics()}</h2>
          <BasicsForm
            {name}
            {draft}
            agent={stored.document.agent}
            timeZone={ctx.timeZone}
            problems={visible.filter(isBasic)}
            disabled={readonly}
            onname={(next) => (name = next)}
            onchange={(next) => (draft = next)}
            ontouch={touchSetting}
          />
        </section>

        <section bind:this={defaults} class="card" aria-labelledby="{uid}-defaults">
          <h2 id="{uid}-defaults">{m.editor_ask_defaults()}</h2>
          <ApprovalFields
            approval={draft.approval}
            {people}
            timeoutError={visible.find((p) => p.rule === null && p.part === 'timeout')?.text ?? ''}
            approversError={visible.find((p) => p.rule === null && p.part === 'approvers')?.text ?? ''}
            disabled={readonly}
            onchange={(approval) => draft && (draft = withDefaults(draft, approval))}
            ontouch={touchSetting}
          />
        </section>

        <section class="rules" aria-labelledby="{uid}-rules">
          <div class="rules-head">
            <h2 id="{uid}-rules">{m.editor_rules()} <span class="n">({new Intl.NumberFormat(ctx.locale).format(draft.rules.length)})</span></h2>
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
                text={texts[index] ?? ruleText(rule, catalog, ctx.locale)}
                decision={rule.decision}
                notes={ruleNotes(draft, index, catalog, overridden[index] ?? null, ctx.locale)}
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
                  locale={ctx.locale}
                  timeZone={ctx.timeZone}
                  onchange={(next) => changeRule(index, next)}
                  ontouch={() => touch(key)}
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
      <PreviewMatrix
        {draft}
        previous={base.draft}
        {version}
        {catalog}
        locale={ctx.locale}
        grid={desktop.matches}
        invalid={problems.some((p) => p.part !== NAME_PART)}
        {catalogMissing}
        {notInEffect}
      />
    </div>
  </div>

  <SaveDialog
    open={saveOpen}
    title={m.save_title()}
    body={m.save_body({ agent, version: version + 1 })}
    confirm={m.save_confirm({ version: version + 1 })}
    prev={base}
    next={edited}
    {catalog}
    people={peopleNames}
    {ctx}
    critical={needsCriticalConfirmation(base.draft, draft)}
    unknown={catalogMissing}
    busy={saving}
    error={saveError}
    onclose={() => (saveOpen = false)}
    onconfirm={() => void save()}
  />
{/if}

<style>
  .head {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .back {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-block-size: 36px;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-accent-text);
    text-decoration: none;
  }
  .back:hover {
    text-decoration: underline;
  }
  .bar,
  .title,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
  }
  .bar {
    gap: var(--hm-space-3) var(--hm-space-4);
  }
  .title {
    flex: 1 1 320px;
    min-inline-size: 0;
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-subtle);
  }
  .state.dirty {
    color: var(--hm-color-text);
  }
  .mark {
    inline-size: 8px;
    block-size: 8px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-positive-fg);
  }
  .dirty .mark {
    background: var(--hm-color-accent);
  }
  .versions {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding-inline: 14px;
    border-radius: var(--hm-radius-md);
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    text-decoration: none;
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  .versions:hover {
    background: var(--hm-color-surface-hover);
  }
  .problems,
  .conflict {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: 10px;
    font-size: 15px;
  }
  .problems {
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-danger-bg);
    border: var(--hm-border-width) solid var(--hm-color-danger-border);
  }
  .count,
  .lead {
    display: flex;
    align-items: center;
    gap: 10px;
    font-weight: var(--hm-font-weight-medium);
  }
  .problems ul {
    margin: 0;
    padding-inline-start: 30px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .problems button {
    min-block-size: 32px;
    padding: 0;
    border: none;
    background: transparent;
    font: inherit;
    font-size: var(--hm-font-size-sm);
    text-align: start;
    text-decoration: underline;
    color: inherit;
    cursor: pointer;
  }
  .conflict {
    color: var(--hm-color-text);
    background: var(--hm-color-warning-bg);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
  }
  .lead {
    color: var(--hm-color-warning-fg);
  }
  .choices {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
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
  @media (pointer: coarse), (max-width: 767px) {
    .back,
    .problems button {
      min-block-size: var(--hm-size-touch);
    }
  }
  @media (max-width: 767px) {
    .state {
      flex: 1 1 100%;
    }
    .versions,
    .actions :global(.btn) {
      flex: 1 1 0;
      justify-content: center;
    }
    .actions {
      flex: 1 1 100%;
    }
  }
</style>
