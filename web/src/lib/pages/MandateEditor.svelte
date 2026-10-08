<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Mandate editor (design README 6.5–6.6). It edits a local draft of the stored version and
  shows live what the draft would mean: computed notes per rule and the preview matrix,
  both from the UI's own evaluation. Saving goes through a summary and never overwrites
  silently: if someone else stored a version meanwhile, the editor says so and keeps the
  edit. The server checks every version again and decides every real request itself.
-->
<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { ApiError } from '../api/client.ts';
  import type { ApproverList, DeviceCatalog, MandateDetail, MandateDraft, Rename } from '../api/types.ts';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import Banner from '../components/Banner.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import BackLink from '../components/BackLink.svelte';
  import ConflictNotice from '../components/mandate/ConflictNotice.svelte';
  import EditorHeader from '../components/mandate/EditorHeader.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import BasicsForm from '../components/mandate/BasicsForm.svelte';
  import DraftWorkspace from '../components/mandate/DraftWorkspace.svelte';
  import SaveDialog from '../components/mandate/SaveDialog.svelte';
  import UnsavedBar from '../components/mandate/UnsavedBar.svelte';
  import { needsCriticalConfirmation } from '../engine/vocabulary.ts';
  import { m } from '../i18n.ts';
  import { countChanges, type Edited } from '../mandate/changes.ts';
  import { effectiveStatus, type EffectiveStatus } from '../mandate/dates.ts';
  import { withoutRevokedConfirmations } from '../mandate/edit.ts';
  import { isStale } from '../mandate/notes.ts';
  import { describeProblems, isBasicsProblem, type FieldProblem, type Part } from '../mandate/problems.ts';
  import { currentNumber, draftOf } from '../mandate/versions.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { toasts } from '../ui/toasts.ts';
  import { isolate } from '../untrusted.ts';

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
    renames: Rename[];
  }

  let { app, id, now }: Props = $props();

  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const NO_APPROVERS: ApproverList = { approvers: [], candidates: { people: [], devices: [] }, version: '' };
  const NOT_IN_EFFECT: Record<EffectiveStatus, () => string> = {
    active: () => '',
    not_yet_valid: () => m.status_not_yet_valid(),
    expired: () => m.status_expired(),
    revoked: () => m.status_revoked(),
  };
  const NAME_PART: Part = 'name';
  const page = new Loader<Data>(async () => {
    const api = app.api;
    const [detail, catalog, approvers, renames] = await Promise.all([
      api.mandate(id),
      // Without Home Assistant the editor shows ids instead of names; rules stay editable.
      api.devices().catch(() => null),
      api.approvers().catch(() => NO_APPROVERS),
      api.renames().catch((): Rename[] => []),
    ]);
    return { detail, catalog, approvers, renames };
  });

  /** The version the edit is based on. */
  let stored = $state.raw<MandateDetail | null>(null);
  /** A newer version on the server while this edit has unsaved changes. */
  let newer = $state.raw<MandateDetail | null>(null);
  let name = $state('');
  let draft = $state.raw<MandateDraft | null>(null);
  let attempted = $state(false);
  const touched = new SvelteSet<string>();
  let saveOpen = $state(false);
  let saving = $state(false);
  let saveError = $state('');
  /** A reload asked for while a save was running; it runs afterwards. */
  let reloadPending = false;
  /** The edit was left earlier and picked up again (app.unsaved): not in force yet (issue #20). */
  let restored = $state(false);
  let workspace: DraftWorkspace | undefined = $state();
  let summary: HTMLElement | undefined = $state();
  let heading: HTMLElement | undefined = $state();

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

  const problems = $derived(draft ? describeProblems(name, draft) : []);
  const ruleKey = (index: number) => `rule:${draft?.rules[index]?.id ?? index}`;
  /** What must have been left for a problem to show: the rule's form, or the field of a setting. */
  const fieldOf = (p: FieldProblem) => (p.rule !== null ? ruleKey(p.rule) : `setting:${p.part}`);
  const visible = $derived(problems.filter((p) => attempted || touched.has(fieldOf(p))));
  /** Former IDs of renamed devices nobody resolved yet → current IDs (rules on them keep applying). */
  const renamed = $derived(new Map((page.data?.renames ?? []).flatMap((r) => r.formers.map((f) => [f, r.entity_id] as const))));
  /** Rules on devices or areas Home Assistant does not have (any more) and no open rename covers. */
  const staleCount = $derived(
    draft && !catalogMissing && !readonly
      ? draft.rules.filter((r) => isStale(r, catalog) && !(r.resource.entity_id !== undefined && renamed.has(r.resource.entity_id))).length
      : 0,
  );
  const storedInvalid = $derived(base ? describeProblems(base.name, base.draft).some((p) => p.part !== 'name') : false);

  /** People for the approver chips: those set up, and whoever a version still names. */
  const people = $derived.by(() => {
    const approvers = page.data?.approvers ?? NO_APPROVERS;
    const names = new Map([...approvers.candidates.people, ...approvers.approvers].map((p) => [p.user_id, p.name]));
    const named = [draft, base?.draft].flatMap((d) => (d ? [...d.approval.approvers, ...d.rules.flatMap((r) => r.approval?.approvers ?? [])] : []));
    const ids = [...new Set([...approvers.approvers.map((a) => a.user_id), ...named])];
    return ids.map((user) => ({ id: user, name: names.get(user) ?? user }));
  });
  const peopleNames = $derived(new Map(people.map((p) => [p.id, p.name])));

  // An unsaved edit survives leaving the page (versions, audit log) while the app is open.
  $effect(() => {
    if (stored && draft && changes > 0) app.unsaved.set(id, { stored, name, draft });
    else if (stored) app.unsaved.delete(id);
  });

  function adopt(detail: MandateDetail) {
    // An undo belongs to the edit that is replaced here; it must not reach into the new one.
    workspace?.forget();
    stored = detail;
    newer = null;
    name = detail.summary.name;
    draft = draftOf(detail.document);
    attempted = false;
    touched.clear();
    restored = false;
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
      restored = true;
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

  /** refreshCatalog reloads only the devices: a rename in Home Assistant must not touch the edit. */
  async function refreshCatalog() {
    const [fresh, renames] = await Promise.all([app.api.devices().catch(() => null), app.api.renames().catch(() => null)]);
    const data = page.data;
    if (data) page.set({ ...data, catalog: fresh ?? data.catalog, renames: renames ?? data.renames });
  }

  onMount(() => {
    const stop = [
      app.on('mandates.changed', (event) => {
        if (event.id === id) void reload();
      }),
      app.on('approvers.changed', () => void reload()),
      app.on('devices.changed', () => void refreshCatalog()),
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
      page.set({ detail, catalog, approvers: page.data?.approvers ?? NO_APPROVERS, renames: page.data?.renames ?? [] });
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
    if (result.dropped) void workspace?.announce(m.critical_override_reset());
  }

  /** The undo of the last discard, while nothing changed since: the draft it left. */
  let discarded = $state.raw<{ toast: number; draft: MandateDraft } | null>(null);
  // Any change after discarding (an edit, a newer version) ends the undo.
  $effect(() => {
    if (discarded && draft !== discarded.draft) dropDiscardUndo();
  });
  onDestroy(() => dropDiscardUndo());

  function dropDiscardUndo() {
    if (discarded) toasts.dismiss(discarded.toast);
    discarded = null;
  }

  /**
   * discard goes back to the stored version the edit was based on, with an undo while
   * nothing changed since. The focus moves to the heading: the bar's button is gone.
   */
  async function discard() {
    if (!stored || !draft || changes === 0) return;
    const before = { name, draft };
    adopt(stored);
    const toast = toasts.show({
      kind: 'undo',
      text: m.unsaved_discarded_toast(),
      action: {
        label: m.common_undo(),
        run: () => {
          if (discarded?.toast !== toast) return;
          discarded = null;
          name = before.name;
          draft = before.draft;
        },
      },
    });
    discarded = { toast, draft: draft as MandateDraft };
    await tick();
    heading?.focus();
  }

  function keydown(event: KeyboardEvent) {
    if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 's') return;
    event.preventDefault();
    void trySave();
  }

  const touchSetting = (part: Part) => touch(`setting:${part}`);
</script>

<!-- Leaving the app with unsaved changes asks first: App.svelte, for every mandate and template. -->
<svelte:window onkeydown={keydown} />

{#if page.status === 'error' && page.code === 'not_found'}
  <EmptyState icon="search" title={m.editor_not_found_title()} body={m.editor_not_found_body()}>
    {#snippet action()}<BackLink href={href({ name: 'mandates' })} label={m.editor_back()} />{/snippet}
  </EmptyState>
{:else if page.status === 'error'}
  <ErrorState title={m.editor_error_title()} body={m.mandates_error_body()} onretry={() => void reload()} />
{:else if !stored || !draft || !base || !edited}
  <div role="status" aria-busy="true" aria-label={m.common_loading()}><Skeleton lines={['30%', '70%', '50%']} /></div>
{:else}
  <EditorHeader
    backHref={href({ name: 'mandates' })}
    versionsHref={href({ name: 'mandate_versions', id })}
    {name}
    storedName={stored.summary.name}
    status={effectiveStatus({ status: stored.summary.status, valid_from: base.draft.valid_from, expires: base.draft.expires }, serverNow)}
    {version}
    digest={stored.summary.digest}
    {readonly}
    {changes}
    idle={changes === 0 && problems.length === 0}
    problems={visible}
    onsave={() => void trySave()}
    onshow={(p) => void workspace?.show(p)}
    bind:summary
    bind:heading
  />

  {#if restored && changes > 0}
    <Banner kind="warning" title={m.unsaved_restored_title()} body={m.unsaved_restored_mandate()} />
  {/if}

  {#if readonly}
    <Banner kind="info" body={m.editor_revoked_note()} />
  {:else if storedInvalid}
    <Banner kind="critical" body={m.code_invalid_mandate()} />
  {/if}
  {#if staleCount > 0}
    <Banner kind="warning" quiet title={m.editor_stale_title()} body={m.editor_stale_body({ count: staleCount })} />
  {/if}
  {#if newer}
    <ConflictNotice version={currentNumber(newer.versions)} onkeep={rebase} ondiscard={() => newer && adopt(newer)} />
  {/if}

  <DraftWorkspace
    bind:this={workspace}
    {draft}
    previous={base.draft}
    {version}
    {catalog}
    {catalogMissing}
    {readonly}
    {people}
    {agent}
    locale={ctx.locale}
    timeZone={ctx.timeZone}
    {visible}
    invalid={problems.some((p) => p.part !== NAME_PART)}
    {renamed}
    {notInEffect}
    settingsTitle={m.editor_basics()}
    onchange={(next) => (draft = next)}
    ontouch={touch}
    unsavedHint={(n) => m.unsaved_rule_hint_mandate({ n })}
    onsave={() => void trySave()}
  >
    {#snippet settings()}
      {#if draft && stored}
        <BasicsForm
          {name}
          {draft}
          agent={stored.document.agent}
          timeZone={ctx.timeZone}
          problems={visible.filter(isBasicsProblem)}
          disabled={readonly}
          onname={(next) => (name = next)}
          onchange={(next) => (draft = next)}
          ontouch={touchSetting}
        />
      {/if}
    {/snippet}
  </DraftWorkspace>

  {#if !readonly && changes > 0}
    <UnsavedBar count={changes} note={m.unsaved_bar_note_mandate()} saveLabel={m.unsaved_bar_save()} onsave={() => void trySave()} ondiscard={() => void discard()} />
  {/if}

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
