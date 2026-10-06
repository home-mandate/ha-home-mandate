<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Template editor (Mandates → Templates; docs/ARCHITECTURE.md section 6): the mandate
  editor's rules, approval defaults and live preview for a template, without agent, name of
  a mandate or validity (admission fills those in). Approvers may keep or add the
  placeholder "the household's approvers and whoever admits the agent". The household's own
  templates are saved in place, naming the version the edit started from: if someone else
  saved meanwhile, nothing is overwritten and the editor offers to reload. Base templates
  are never changed; any template can be saved under a new name, base templates hidden.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { ApiError } from '../api/client.ts';
  import type { ApproverList, Defaults, DeviceCatalog, MandateDraft, Template, TemplateSummary } from '../api/types.ts';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import BackLink from '../components/BackLink.svelte';
  import Banner from '../components/Banner.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import BasicsForm from '../components/mandate/BasicsForm.svelte';
  import DraftWorkspace from '../components/mandate/DraftWorkspace.svelte';
  import SaveDialog from '../components/mandate/SaveDialog.svelte';
  import TemplateDeleteDialog from '../components/mandate/TemplateDeleteDialog.svelte';
  import TemplateHeader from '../components/mandate/TemplateHeader.svelte';
  import TemplateLoadDialog from '../components/mandate/TemplateLoadDialog.svelte';
  import TemplateSaveAsDialog from '../components/mandate/TemplateSaveAsDialog.svelte';
  import { needsCriticalConfirmation } from '../engine/vocabulary.ts';
  import { m } from '../i18n.ts';
  import { countChanges, type Edited } from '../mandate/changes.ts';
  import { APPROVERS_PLACEHOLDER, isPlaceholder } from '../mandate/placeholder.ts';
  import { describeProblems, isBasicsProblem, type FieldProblem, type Part } from '../mandate/problems.ts';
  import { NEW_TEMPLATE_KEY, RESERVED_PREFIX, templateDescription, templateTitle, type NameProblem } from '../mandate/template.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { toasts } from '../ui/toasts.ts';
  import { isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
    /** Name of the template; null for a new one. */
    template: string | null;
    /** Browser clock, ticking. */
    now: number;
  }

  /** The template was removed while the editor showed it. */
  const GONE = 'gone';

  interface Data {
    /** null for a new template. */
    stored: Template | null | typeof GONE;
    list: TemplateSummary[];
    /** null: the device list could not be loaded. */
    catalog: DeviceCatalog | null;
    approvers: ApproverList;
    defaults: Defaults;
  }

  let { app, template: name, now }: Props = $props();

  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const NO_APPROVERS: ApproverList = { approvers: [], candidates: { people: [], devices: [] }, version: '' };
  /** As the server's defaults, when they cannot be read. */
  const FALLBACK_DEFAULTS: Defaults = { approval_timeout: 'PT2M', max_actions_per_hour: 60, bell: false };
  const NAME_PART: Part = 'name';
  const NO_RENAMES: ReadonlyMap<string, string> = new Map();

  const page: Loader<Data> = new Loader<Data>(async (): Promise<Data> => {
    const api = app.api;
    const [stored, list, catalog, approvers, defaults] = await Promise.all([
      name === null
        ? null
        : api.template(name).catch((err: unknown): typeof GONE => {
            // Removed while open: the edit stays and can be saved as a new template.
            if (page.data && err instanceof ApiError && err.code === 'not_found') return GONE;
            throw err;
          }),
      api.templates().catch(() => page.data?.list ?? []),
      // Without Home Assistant the editor shows ids instead of names; rules stay editable.
      api.devices().catch(() => null),
      api.approvers().catch(() => NO_APPROVERS),
      api.settings().catch(() => FALLBACK_DEFAULTS),
    ]);
    return { stored, list, catalog, approvers, defaults };
  });

  /** The template the edit is based on; null for a new one. */
  let stored = $state.raw<Template | null>(null);
  /** The draft the edit started from: the stored one, or an empty one for a new template. */
  let origin = $state.raw<MandateDraft | null>(null);
  let draft = $state.raw<MandateDraft | null>(null);
  /** A newer version on the server while this edit has unsaved changes, or the template is gone. */
  let newer = $state.raw<Template | typeof GONE | null>(null);
  let attempted = $state(false);
  const touched = new SvelteSet<string>();
  let saveOpen = $state(false);
  let saving = $state(false);
  let saveError = $state('');
  let saveAsOpen = $state(false);
  let saveAsError = $state('');
  let refused = $state<NameProblem | null>(null);
  let deleteOpen = $state(false);
  let deleteError = $state('');
  let loadOpen = $state(false);
  let busy = $state(false);
  let workspace: DraftWorkspace | undefined = $state();
  let summary: HTMLElement | undefined = $state();

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const catalog = $derived(page.data?.catalog ?? NO_CATALOG);
  const catalogMissing = $derived(page.data !== null && page.data.catalog === null);
  const own = $derived(stored !== null && !stored.builtin);
  const title = $derived(stored ? templateTitle(stored) : m.template_new_title());
  /** Problems of a mandate's name do not apply: the title stands in for it. */
  const base: Edited | null = $derived(origin ? { name: title, draft: origin } : null);
  const edited: Edited | null = $derived(draft ? { name: title, draft } : null);
  const changes = $derived(base && edited ? countChanges(base, edited) : 0);
  const saveable = $derived(own && newer === null);
  /** Where the unsaved edit is kept while the app is open. */
  const key = $derived(name ?? NEW_TEMPLATE_KEY);
  const dialogOpen = $derived(saveOpen || saveAsOpen || deleteOpen || loadOpen);

  const problems = $derived(draft ? describeProblems(title, draft).filter((p) => p.part !== NAME_PART) : []);
  const ruleKey = (index: number) => `rule:${draft?.rules[index]?.id ?? index}`;
  /** What must have been left for a problem to show: the rule's form, or the field of a setting. */
  const fieldOf = (p: FieldProblem) => (p.rule !== null ? ruleKey(p.rule) : `setting:${p.part}`);
  const visible = $derived(problems.filter((p) => attempted || touched.has(fieldOf(p))));

  /** People for the approver chips: the placeholder, those set up, and whoever the template names. */
  const people = $derived.by(() => {
    const approvers = page.data?.approvers ?? NO_APPROVERS;
    const names = new Map([...approvers.candidates.people, ...approvers.approvers].map((p) => [p.user_id, p.name]));
    const named = [draft, origin].flatMap((d) => (d ? [...d.approval.approvers, ...d.rules.flatMap((r) => r.approval?.approvers ?? [])] : []));
    const ids = [...new Set([APPROVERS_PLACEHOLDER, ...approvers.approvers.map((a) => a.user_id), ...named])];
    return ids.map((user) => ({ id: user, name: isPlaceholder(user) ? m.tpl_approvers_placeholder() : (names.get(user) ?? user) }));
  });
  const peopleNames = $derived(new Map(people.map((p) => [p.id, p.name])));
  const agent = $derived(m.template_agent());

  /** An empty template: everything forbidden, asking the placeholder, the household's defaults. */
  function emptyDraft(defaults: Defaults): MandateDraft {
    const from = new Date(now - app.offsetMs).toISOString().replace(/\.\d+Z$/, 'Z');
    return {
      rules: [],
      approval: { timeout: defaults.approval_timeout, approvers: [APPROVERS_PLACEHOLDER] },
      limits: { max_actions_per_hour: defaults.max_actions_per_hour },
      valid_from: from,
    };
  }

  function adopt(t: Template | null, defaults: Defaults) {
    // An undo belongs to the edit that is replaced here; it must not reach into the new one.
    workspace?.forget();
    stored = t;
    origin = t ? t.draft : emptyDraft(defaults);
    draft = origin;
    newer = null;
    attempted = false;
    touched.clear();
  }

  // An unsaved edit survives leaving the page (back link, navigation) while the app is open.
  $effect(() => {
    if (origin && draft && changes > 0) app.unsavedTemplates.set(key, { stored, origin, draft });
    else if (origin) app.unsavedTemplates.delete(key);
  });

  /** incoming handles what a load brought, at first and while the editor is open. */
  function incoming(data: Data) {
    const fresh = data.stored;
    if (origin === null) {
      // First load: pick up an edit left behind earlier, then compare it with what is stored now.
      const kept = app.unsavedTemplates.get(key);
      if (!kept) {
        adopt(fresh === GONE ? null : fresh, data.defaults);
        return;
      }
      stored = kept.stored;
      origin = kept.origin;
      draft = kept.draft;
    }
    if (fresh === null) return; // a new template: nothing on the server yet
    if (fresh === GONE) {
      newer = GONE;
      return;
    }
    if (stored && fresh.digest === stored.digest) {
      // The same version; whether it is hidden may have changed.
      if (fresh.hidden !== stored.hidden) stored = fresh;
      newer = null;
      return;
    }
    if (changes > 0) {
      newer = fresh;
      return;
    }
    adopt(fresh, data.defaults);
    toasts.show({ kind: 'success', text: m.template_updated_toast() });
  }

  async function reload() {
    if (saving) return;
    await page.run();
    if (page.data && !saving) incoming(page.data);
  }

  // Someone else's version stays a conflict only while there is something to lose.
  $effect(() => {
    if (newer && newer !== GONE && changes === 0 && page.data) adopt(newer, page.data.defaults);
  });

  onMount(() => {
    const stop = [
      app.on('templates.changed', () => void reload()),
      app.on('approvers.changed', () => void reload()),
      app.on('devices.changed', () => void reload()),
      app.on('reconnected', () => void reload()),
    ];
    void reload();
    return () => stop.forEach((off) => off());
  });

  function touch(key: string) {
    touched.add(key);
  }

  const touchSetting = (part: Part) => touch(`setting:${part}`);

  /** blocked shows the problems instead of a dialog; true when there are any. */
  async function blocked(): Promise<boolean> {
    if (problems.length === 0) return false;
    attempted = true;
    await tick();
    summary?.focus();
    return true;
  }

  async function trySave() {
    if (!saveable || !draft || saveOpen || (await blocked()) || changes === 0) return;
    saveError = '';
    saveOpen = true;
  }

  async function trySaveAs() {
    if (!draft || saveAsOpen || (await blocked())) return;
    saveAsError = '';
    refused = null;
    saveAsOpen = true;
  }

  /** rejected words a refusal of the server that is not about the version or the name. */
  function rejected(err: unknown): string {
    const code = err instanceof ApiError ? err.code : 'internal';
    return code === 'invalid_mandate' || code === 'critical_confirmation_required' ? m.template_save_rejected() : m.toast_save_failed();
  }

  async function save() {
    if (!stored || !base || !draft || saving || !saveable) return;
    saving = true;
    saveError = '';
    let conflict = false;
    try {
      const saved = await app.api.putTemplate(stored.name, {
        draft,
        base_digest: stored.digest,
        ...(needsCriticalConfirmation(base.draft, draft) ? { confirm_critical: true } : {}),
      });
      if (page.data) page.set({ ...page.data, stored: saved });
      adopt(saved, page.data?.defaults ?? FALLBACK_DEFAULTS);
      saveOpen = false;
      toasts.show({ kind: 'success', text: m.template_saved_toast() });
    } catch (err) {
      // Changed or deleted meanwhile: the reload shows which, and the edit stays.
      conflict = err instanceof ApiError && (err.code === 'conflict' || err.code === 'not_found');
      if (!conflict) saveError = rejected(err);
    } finally {
      saving = false;
    }
    if (conflict) {
      // Never overwrite: show what is there now and offer to reload.
      saveOpen = false;
      await reload();
      if (!newer) toasts.show({ kind: 'error', text: m.toast_save_failed() });
    }
  }

  async function saveAs(target: string) {
    if (!draft || saving) return;
    saving = true;
    saveAsError = '';
    refused = null;
    try {
      await app.api.putTemplate(target, {
        draft,
        base_digest: null,
        ...(needsCriticalConfirmation(null, draft) ? { confirm_critical: true } : {}),
      });
      saveAsOpen = false;
      // The edit is stored under the new name: nothing is left unsaved here.
      draft = origin;
      app.unsavedTemplates.delete(key);
      toasts.show({ kind: 'success', text: m.template_saved_as_toast({ name: isolate(target) }) });
      window.location.hash = href({ name: 'template', template: target });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'internal';
      if (code === 'conflict') refused = 'taken';
      else if (code === 'builtin_template') refused = 'reserved';
      else if (code === 'invalid_input' && err instanceof ApiError && err.field === '/name') refused = target.startsWith(RESERVED_PREFIX) ? 'reserved' : 'format';
      else saveAsError = rejected(err);
    } finally {
      saving = false;
    }
  }

  async function remove() {
    if (!stored || !own || busy) return;
    busy = true;
    deleteError = '';
    try {
      await app.api.deleteTemplate(stored.name);
    } catch (err) {
      // Removed by someone else already: the outcome is the same.
      if (!(err instanceof ApiError && err.code === 'not_found')) {
        deleteError = m.template_delete_failed();
        busy = false;
        return;
      }
    }
    busy = false;
    deleteOpen = false;
    app.unsavedTemplates.delete(key);
    toasts.show({ kind: 'success', text: m.template_deleted_toast({ name: isolate(stored.name) }) });
    window.location.hash = href({ name: 'templates' });
  }

  async function setHidden(hidden: boolean) {
    if (!stored?.builtin || busy) return;
    busy = true;
    try {
      await app.api.setTemplateHidden(stored.name, hidden);
      stored = { ...stored, hidden };
      toasts.show({ kind: 'success', text: hidden ? m.template_hidden_toast() : m.template_shown_toast() });
    } catch {
      toasts.show({ kind: 'error', text: m.template_hide_failed() });
    } finally {
      busy = false;
    }
  }

  function keydown(event: KeyboardEvent) {
    if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 's') return;
    event.preventDefault();
    // Never a second dialog over an open one (delete, load, save).
    if (dialogOpen) return;
    void (saveable ? trySave() : trySaveAs());
  }

  function beforeunload(event: BeforeUnloadEvent) {
    if (changes > 0) event.preventDefault();
  }
</script>

<svelte:window onkeydown={keydown} onbeforeunload={beforeunload} />

{#if page.status === 'error' && page.code === 'not_found'}
  <EmptyState icon="search" title={m.template_not_found_title()} body={m.template_not_found_body()}>
    {#snippet action()}<BackLink href={href({ name: 'templates' })} label={m.templates_title()} />{/snippet}
  </EmptyState>
{:else if page.status === 'error'}
  <ErrorState title={m.templates_error_title()} body={m.templates_error_body()} onretry={() => void reload()} />
{:else if !draft || !base || !edited || !page.data}
  <div role="status" aria-busy="true" aria-label={m.common_loading()}><Skeleton lines={['30%', '70%', '50%']} /></div>
{:else}
  <TemplateHeader
    backHref={href({ name: 'templates' })}
    {title}
    description={stored ? templateDescription(stored) : ''}
    template={stored}
    {changes}
    {saveable}
    problems={visible}
    {busy}
    onload={() => (loadOpen = true)}
    onsave={() => void trySave()}
    onsaveas={() => void trySaveAs()}
    ondelete={() => {
      deleteError = '';
      deleteOpen = true;
    }}
    onhidden={(hidden) => void setHidden(hidden)}
    onshow={(p) => void workspace?.show(p)}
    bind:summary
  />

  {#if stored?.builtin}
    <Banner kind="info" quiet body={stored.hidden ? m.template_builtin_hidden_note() : m.template_builtin_note()} />
  {/if}
  {#if newer === GONE}
    <Banner kind="warning" title={m.template_gone_title()} body={m.template_gone_body()} />
  {:else if newer}
    <Banner
      kind="warning"
      title={m.template_conflict_title()}
      body={m.template_conflict_body()}
      action={{ label: m.template_conflict_reload(), onclick: () => newer && newer !== GONE && adopt(newer, page.data?.defaults ?? FALLBACK_DEFAULTS) }}
    />
  {/if}

  <DraftWorkspace
    bind:this={workspace}
    {draft}
    previous={base.draft}
    version={null}
    {catalog}
    {catalogMissing}
    readonly={false}
    {people}
    {agent}
    locale={ctx.locale}
    timeZone={ctx.timeZone}
    {visible}
    invalid={problems.length > 0}
    renamed={NO_RENAMES}
    settingsTitle={m.editor_basics()}
    onchange={(next) => (draft = next)}
    ontouch={touch}
  >
    {#snippet settings()}
      {#if draft}
        <BasicsForm
          name={title}
          {draft}
          timeZone={ctx.timeZone}
          problems={visible.filter(isBasicsProblem)}
          disabled={false}
          onname={() => {}}
          onchange={(next) => (draft = next)}
          ontouch={touchSetting}
        />
      {/if}
    {/snippet}
  </DraftWorkspace>

  <SaveDialog
    open={saveOpen}
    title={m.save_title()}
    body={m.template_save_body()}
    confirm={m.template_save_confirm()}
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
  <TemplateSaveAsDialog
    open={saveAsOpen}
    existing={page.data.list}
    critical={needsCriticalConfirmation(null, draft)}
    busy={saving}
    {refused}
    error={saveAsError}
    onedit={() => (refused = null)}
    onclose={() => (saveAsOpen = false)}
    onconfirm={(target) => void saveAs(target)}
  />
  <TemplateDeleteDialog open={deleteOpen} {title} {busy} error={deleteError} onclose={() => (deleteOpen = false)} ondelete={() => void remove()} />
  <TemplateLoadDialog open={loadOpen} templates={page.data.list} current={name} {changes} onclose={() => (loadOpen = false)} />
{/if}
