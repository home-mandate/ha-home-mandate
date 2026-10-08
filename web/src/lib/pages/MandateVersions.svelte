<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Versions of a mandate (design README 6.7): who stored which version when, and a compare
  of an earlier version with the current one: both rule lists side by side with added /
  changed / removed marks, plus the computed effect on permissions. Restoring never
  overwrites: it stores a new version with the old content and goes through the same
  summary as saving.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError } from '../api/client.ts';
  import type { ApproverList, DeviceCatalog, MandateDetail, MandateDocument, MandateVersion, Rule, TemplateSummary } from '../api/types.ts';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import BackLink from '../components/BackLink.svelte';
  import Icon from '../components/Icon.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import EffectsList from '../components/mandate/EffectsList.svelte';
  import RuleSentence from '../components/mandate/RuleSentence.svelte';
  import SaveDialog from '../components/mandate/SaveDialog.svelte';
  import { needsCriticalConfirmation } from '../engine/vocabulary.ts';
  import { formatDateTime } from '../format.ts';
  import { m } from '../i18n.ts';
  import { ruleChanges, type Edited, type RuleChangeKind } from '../mandate/changes.ts';
  import { versionOriginText } from '../mandate/origin.ts';
  import { settingLines } from '../mandate/summary.ts';
  import { ruleText } from '../mandate/text.ts';
  import { currentNumber, draftOf, restoredDraft, shortDigest, versionAt } from '../mandate/versions.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { toasts } from '../ui/toasts.ts';
  import { cleanUntrusted, isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
    id: string;
  }

  interface Data {
    detail: MandateDetail;
    /** null: the device list could not be loaded. */
    catalog: DeviceCatalog | null;
    approvers: ApproverList;
    /** For the titles of base templates the versions came from; empty if they could not be loaded. */
    templates: TemplateSummary[];
  }

  let { app, id }: Props = $props();

  const uid = $props.id();
  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const NO_APPROVERS: ApproverList = { approvers: [], candidates: { people: [], devices: [] }, version: '' };
  const TAGS: Record<RuleChangeKind, () => string> = {
    added: () => m.version_rule_added(),
    changed: () => m.version_rule_changed(),
    removed: () => m.version_rule_removed(),
  };

  const page = new Loader<Data>(async () => {
    const api = app.api;
    const [detail, catalog, approvers, templates] = await Promise.all([
      api.mandate(id),
      api.devices().catch(() => null),
      api.approvers().catch(() => NO_APPROVERS),
      api.templates().catch((): TemplateSummary[] => []),
    ]);
    return { detail, catalog, approvers, templates };
  });

  /**
   * Number of the earlier version that is compared with the current one. Not its digest:
   * a digest is a hash of the content, and a restored version repeats an earlier one.
   */
  let picked = $state<number | null>(null);
  const earlier = new Loader<MandateDocument | null>(async () => (picked === null ? null : app.api.mandateVersion(id, picked)));
  let restoring = $state(false);
  let saving = $state(false);
  let saveError = $state('');

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const detail = $derived(page.data?.detail ?? null);
  const catalog = $derived(page.data?.catalog ?? NO_CATALOG);
  const catalogMissing = $derived(page.data !== null && page.data.catalog === null);
  const versions = $derived(detail?.versions ?? []);
  const pickedVersion = $derived(picked === null ? undefined : versionAt(versions, picked));
  /** Restoring a version with the content of the current one would store nothing. */
  const identical = $derived(pickedVersion !== undefined && pickedVersion.digest === versions[0]?.digest);
  const current: Edited | null = $derived(detail ? { name: detail.summary.name, draft: draftOf(detail.document) } : null);
  const old: Edited | null = $derived(detail && earlier.data ? { name: detail.summary.name, draft: draftOf(earlier.data) } : null);
  /** What restoring stores: the earlier rules with today's name and validity. */
  const restored: Edited | null = $derived(current && old ? { name: current.name, draft: restoredDraft(current.draft, old.draft) } : null);
  const kinds = $derived(new Map(old && current ? ruleChanges(old.draft, current.draft).map((c) => [c.rule.id, c.kind]) : []));
  const people = $derived.by(() => {
    const approvers = page.data?.approvers ?? NO_APPROVERS;
    return new Map([...approvers.candidates.people, ...approvers.approvers].map((p) => [p.user_id, p.name]));
  });
  const settings = $derived(old && current ? settingLines(old, current, ctx, people) : []);
  const agent = $derived(isolate(detail?.document.agent.display_name));

  const author = (v: MandateVersion) => cleanUntrusted(v.created_by_name) || m.versions_unknown_author();
  const when = (v: MandateVersion) => formatDateTime(new Date(v.created_at), ctx);
  const originOf = (v: MandateVersion) => versionOriginText(v, page.data?.templates ?? []);

  /** pick loads an earlier version for the compare; picking it again retries a failed load. */
  function pick(number: number) {
    if (picked === number && earlier.status !== 'error') return;
    picked = number;
    earlier.data = null;
    void earlier.run();
  }

  async function reload() {
    await page.run();
    const [, ...earlierOnes] = page.data?.detail.versions ?? [];
    // Keep the pick while it is an earlier version; otherwise compare with the one before the current.
    const previous = earlierOnes[0];
    if (!previous) picked = null;
    else if (!earlierOnes.some((v) => v.number === picked)) pick(previous.number);
  }

  onMount(() => {
    const stop = [
      app.on('mandates.changed', (event) => {
        if (event.id === id && !saving) void reload();
      }),
      app.on('reconnected', () => void reload()),
    ];
    void reload();
    return () => stop.forEach((off) => off());
  });

  async function restore() {
    if (!detail || !restored || saving) return;
    saving = true;
    saveError = '';
    let done = false;
    try {
      const stored = await app.api.putMandate(id, {
        name: detail.summary.name,
        draft: restored.draft,
        base_digest: detail.summary.digest,
        ...(current && needsCriticalConfirmation(current.draft, restored.draft) ? { confirm_critical: true } : {}),
      });
      restoring = false;
      done = true;
      toasts.show({ kind: 'success', text: m.toast_saved({ version: currentNumber(stored.versions) }) });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'internal';
      const rejected = code === 'invalid_mandate' || code === 'invalid_input' || code === 'critical_confirmation_required';
      saveError = code === 'conflict' ? m.editor_conflict_title() : rejected ? m.save_rejected() : m.toast_save_failed();
    } finally {
      saving = false;
    }
    await reload();
    // The restored version is now the current one; compare what was current before with it.
    const previous = versions[1];
    if (done && previous) pick(previous.number);
  }

  /** tagOf marks a rule in one of the two lists: "removed" only in the old one, "added" only in the current one. */
  const tagOf = (rule: Rule, isCurrent: boolean) => {
    const kind = kinds.get(rule.id);
    if (kind === undefined || (kind === 'removed' && isCurrent) || (kind === 'added' && !isCurrent)) return '';
    return TAGS[kind]();
  };
  const label = (version: number) => m.version_label({ version });
  const CURRENT = 'true' as const;
  const DOT = ' · ';
</script>

{#snippet rules(list: readonly Rule[], isCurrent: boolean)}
  {#each list as rule (rule.id)}
    {@const text = ruleText(rule, catalog, ctx.locale)}
    {@const tag = tagOf(rule, isCurrent)}
    <li class:marked={tag !== ''}>
      {#if tag}<span class="tag"><Icon name="info" size={16} />{tag}</span>{/if}
      <RuleSentence {text} decision={rule.decision} size="sm" critical={rule.allow_critical === true} />
      {#if text.conditions}<span class="conditions">{text.conditions}</span>{/if}
    </li>
  {/each}
  {#if list.length === 0}<li class="empty">{m.versions_no_rules()}</li>{/if}
{/snippet}

{#if page.status === 'error' && page.code === 'not_found'}
  <EmptyState icon="search" title={m.editor_not_found_title()} body={m.editor_not_found_body()}>
    {#snippet action()}<BackLink href={href({ name: 'mandates' })} label={m.editor_back()} />{/snippet}
  </EmptyState>
{:else if page.status === 'error'}
  <ErrorState title={m.editor_error_title()} body={m.mandates_error_body()} onretry={() => void reload()} />
{:else if !detail || !current}
  <div role="status" aria-busy="true" aria-label={m.common_loading()}><Skeleton lines={['30%', '70%', '50%']} /></div>
{:else}
  {@const current_ = currentNumber(versions)}
  <div class="head">
    <BackLink href={href({ name: 'mandate', id })} label={cleanUntrusted(detail.summary.name)} untrusted />
    <h1>{m.versions_title()}</h1>
  </div>

  <div class="columns">
    <ol class="versions" role="list" aria-label={m.versions_pick()}>
      {#each versions as v, i (v.number)}
        {@const n = v.number}
        <li>
          {#if i === 0}
            <div class="version current">
              <span class="line"><strong>{label(n)}</strong><code title={v.digest}>{shortDigest(v.digest)}</code><span class="pill">{m.version_current()}</span></span>
              <span class="by"><bdi>{author(v)}</bdi> · {when(v)}</span>
              {#if originOf(v)}<span class="origin">{originOf(v)}</span>{/if}
            </div>
          {:else}
            <button type="button" class="version" aria-current={picked === n ? CURRENT : undefined} onclick={() => pick(n)}>
              <span class="line">
                <strong>{label(n)}</strong><code title={v.digest}>{shortDigest(v.digest)}</code>
                {#if picked === n}<span class="pill compare">{m.version_compare()}</span>{/if}
              </span>
              <span class="by"><bdi>{author(v)}</bdi> · {when(v)}</span>
              {#if originOf(v)}<span class="origin">{originOf(v)}</span>{/if}
            </button>
          {/if}
        </li>
      {/each}
    </ol>

    <div class="compare">
      {#if versions.length < 2}
        <p class="note">{m.versions_single()}</p>
      {:else if earlier.status === 'error'}
        <div class="note" role="alert">
          <span>{m.versions_load_failed()}</span>
          <Button icon="refresh" onclick={() => picked !== null && pick(picked)}>{m.common_retry()}</Button>
        </div>
      {:else if !old || picked === null}
        <div role="status" aria-busy="true" aria-label={m.common_loading()}><Skeleton lines={['50%', '80%', '60%']} /></div>
      {:else}
        {@const pickedNumber = picked}
        <div class="compare-head">
          <h2 aria-live="polite">{m.versions_compare_title({ a: label(pickedNumber), b: label(current_) })}</h2>
          {#if detail.summary.status === 'active' && !identical}
            <Button
              size="lg"
              onclick={() => {
                saveError = '';
                restoring = true;
              }}
            >
              {m.version_restore()}
            </Button>
          {/if}
        </div>
        <div class="sides">
          <section aria-labelledby="{uid}-old">
            <h3 id="{uid}-old">{label(pickedNumber)}{#if pickedVersion}{DOT}<bdi>{author(pickedVersion)}</bdi>{/if}</h3>
            <ul role="list">{@render rules(old.draft.rules, false)}</ul>
          </section>
          <section aria-labelledby="{uid}-current">
            <h3 id="{uid}-current">{label(current_)}{#if versions[0]}{DOT}<bdi>{author(versions[0])}</bdi>{/if}</h3>
            <ul role="list">{@render rules(current.draft.rules, true)}</ul>
          </section>
        </div>
        {#if settings.length > 0}
          <section class="card" aria-labelledby="{uid}-settings">
            <h3 id="{uid}-settings" class="strong">{m.save_settings_heading()}</h3>
            {#each settings as line (line.setting)}
              <span class="setting"><strong>{line.label}</strong>{#if line.from || line.to}: <bdi>{line.from}</bdi> → <bdi>{line.to}</bdi>{/if}</span>
            {/each}
          </section>
        {/if}
        <section class="card" aria-labelledby="{uid}-effects">
          <h3 id="{uid}-effects" class="strong">{m.save_effects_heading()}</h3>
          <EffectsList prev={old.draft} next={current.draft} devices={catalog.devices} locale={ctx.locale} unknown={catalogMissing} />
        </section>

        <SaveDialog
          open={restoring}
          title={m.restore_title({ version: pickedNumber })}
          body={m.restore_body({ agent, version: pickedNumber, next: current_ + 1 })}
          confirm={m.save_confirm({ version: current_ + 1 })}
          prev={current}
          next={restored ?? old}
          {catalog}
          {people}
          {ctx}
          critical={restored !== null && needsCriticalConfirmation(current.draft, restored.draft)}
          unknown={catalogMissing}
          busy={saving}
          error={saveError}
          onclose={() => (restoring = false)}
          onconfirm={() => void restore()}
        />
      {/if}
    </div>
  </div>
{/if}

<style>
  .origin {
    display: block;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  .head {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
  }
  .columns {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--hm-space-6);
    align-items: start;
  }
  @media (min-width: 768px) {
    .columns {
      grid-template-columns: minmax(260px, 0.8fr) minmax(0, 2.2fr);
    }
    .sides {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    }
  }
  .versions {
    list-style: none;
    margin: 0;
    padding: var(--hm-space-2);
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .version {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    box-sizing: border-box;
    inline-size: 100%;
    min-block-size: var(--hm-size-touch);
    padding: 10px var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid transparent;
    font: inherit;
    text-align: start;
    color: var(--hm-color-text);
    background: transparent;
  }
  button.version {
    cursor: pointer;
  }
  button.version:hover {
    background: var(--hm-color-surface-hover);
  }
  .version.current,
  .version[aria-current='true'] {
    background: var(--hm-color-accent-subtle);
    border-color: var(--hm-color-info-border);
  }
  .line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px 10px;
    font-size: 15px;
  }
  strong {
    font-weight: var(--hm-font-weight-semibold);
  }
  code {
    font-size: 12px;
    color: var(--hm-color-text-muted);
  }
  .pill {
    padding-inline: var(--hm-space-2);
    border-radius: 6px;
    font-size: 12px;
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-positive-fg);
    background: var(--hm-color-positive-bg);
  }
  .pill.compare {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
    background: var(--hm-color-surface);
  }
  .by {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  .compare {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    min-inline-size: 0;
  }
  .compare-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-2) var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .sides {
    display: grid;
    gap: var(--hm-space-3);
  }
  section {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-inline-size: 0;
    padding: 14px;
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  h3.strong {
    font-size: 15px;
    color: var(--hm-color-text);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  ul li {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    padding: var(--hm-space-2) 10px;
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  ul li.marked {
    background: var(--hm-color-accent-subtle);
    border-color: var(--hm-color-info-border);
  }
  ul li.empty {
    border-style: dashed;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-default-fg);
  }
  .tag {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    font-size: 12px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  .conditions {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .setting {
    padding-block: var(--hm-space-1);
    font-size: var(--hm-font-size-sm);
    overflow-wrap: anywhere;
  }
  .note {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-2) var(--hm-space-4);
    margin: 0;
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    font-size: 15px;
    color: var(--hm-color-text-muted);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) dashed var(--hm-color-border);
  }
</style>
