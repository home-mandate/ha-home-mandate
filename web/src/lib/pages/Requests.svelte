<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Approval requests (design README 6.9): pending ones with countdown on the server clock
  and their history, side by side on desktop, behind a two-tab switch on mobile. A person
  who may answer a request here (decision F2) gets decline and approve; everyone else
  answers on the phone, and the buttons are not shown at all. When a request ends, its card
  leaves, the history grows and a live region says the result.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { slide } from 'svelte/transition';
  import { ApiError } from '../api/client.ts';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { ApprovalHistoryEntry, Approvals, DeviceCatalog } from '../api/types.ts';
  import { historyOutcome } from '../approvals/history.ts';
  import AgentName from '../components/AgentName.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import RequestAnswer from '../components/RequestAnswer.svelte';
  import RequestCard from '../components/RequestCard.svelte';
  import AuditTabs from '../components/audit/AuditTabs.svelte';
  import { formatDateTime } from '../format.ts';
  import { m } from '../i18n.ts';
  import { actionLabel } from '../mandate/labels.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { DESKTOP, Media } from '../ui/media.svelte.ts';
  import { toasts } from '../ui/toasts.ts';
  import { cleanUntrusted, isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
  }

  interface Data {
    approvals: Approvals;
    catalog: DeviceCatalog | null;
  }

  let { app }: Props = $props();

  const LEAVE_MS = 150;
  const SEPARATOR = ' · ';

  const id = $props.id();
  const desktop = new Media(DESKTOP);
  const reduced = new Media('(prefers-reduced-motion: reduce)', false);

  let tab = $state<'open' | 'history'>('open');
  let busy = $state<string | null>(null);
  let spoken = $state('');
  let openHeading: HTMLElement | undefined = $state();
  let openSection: HTMLElement | undefined = $state();
  const TABS = ['open', 'history'] as const;
  const TAB_LABELS: Record<(typeof TABS)[number], () => string> = {
    open: () => m.requests_tab_open(),
    history: () => m.requests_tab_history(),
  };

  const loader = new Loader<Data>(async () => {
    const [approvals, catalog] = await Promise.all([app.api.approvals(), app.api.devices().catch(() => null)]);
    return { approvals, catalog };
  });

  /** announce says the end of a request once; the same text twice in a row is said again. */
  async function announce(entry: ApprovalHistoryEntry) {
    spoken = '';
    await tick();
    spoken = m.request_closed_live({ device: isolate(entry.device_name), result: historyOutcome(entry).text });
  }

  /** keepFocus moves the focus to the section heading when the focused card's request has ended. */
  async function keepFocus() {
    const focused = document.activeElement;
    const card = focused instanceof HTMLElement && openSection?.contains(focused) ? focused.closest<HTMLElement>('[data-request]') : null;
    if (!card || open.some((r) => r.id === card.dataset['request'])) return;
    await tick();
    openHeading?.focus();
  }

  onMount(() => {
    const reload = () => void loader.run();
    const stop = [
      app.on('approval.opened', reload),
      app.on('approval.closed', (event) => {
        void announce(event.entry);
        void loader.run().then(keepFocus);
      }),
      app.on('reconnected', reload),
    ];
    reload();
    return () => stop.forEach((off) => off());
  });

  /**
   * answerError says what a failed answer means. Without an answer from the server (status 0,
   * or no API error at all) the answer may still have arrived, so it does not claim it failed.
   */
  function answerError(err: unknown): string {
    if (!(err instanceof ApiError) || err.status === 0) return m.request_answer_unknown();
    return err.code === 'not_found' ? m.request_gone() : m.request_answer_failed();
  }

  async function answer(requestId: string, approve: boolean) {
    if (busy) return;
    busy = requestId;
    try {
      await app.api.answerApproval(requestId, approve);
    } catch (err) {
      toasts.show({ kind: 'error', text: answerError(err) });
    } finally {
      busy = null;
    }
    await loader.run();
    await keepFocus();
  }

  /** Tabs with arrow keys, Home and End; the selection follows the focus. */
  async function tabKey(event: KeyboardEvent) {
    const at = TABS.indexOf(tab);
    const step = ({ ArrowRight: 1, ArrowLeft: -1, Home: -at, End: TABS.length - 1 - at } as Record<string, number>)[event.key];
    if (step === undefined) return;
    event.preventDefault();
    const arrow = event.key === 'ArrowRight' || event.key === 'ArrowLeft';
    const rtl = arrow && getComputedStyle(event.currentTarget as Element).direction === 'rtl';
    const next = TABS[(at + (rtl ? -step : step) + TABS.length) % TABS.length] ?? 'open';
    tab = next;
    await tick();
    document.getElementById(`${id}-tab-${next}`)?.focus();
  }

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const data = $derived(loader.data);
  const open = $derived(data?.approvals.open ?? []);
  const done = $derived(
    [...(data?.approvals.history ?? [])].sort((a, b) => Date.parse(b.answered_at) - Date.parse(a.answered_at)),
  );
  const answerHere = $derived(open.some((r) => r.can_answer));
  const haDown = $derived(app.system ? !app.system.ha.connected : false);
  const tabs = $derived(!desktop.matches);
  const hideOpen = $derived(tabs && tab !== 'open');
  const hideHistory = $derived(tabs && tab !== 'history');
  const leave = $derived(reduced.matches ? 0 : LEAVE_MS);

  function areaName(areaId: string | null): string | null {
    if (areaId === null) return null;
    return data?.catalog?.areas.find((a) => a.id === areaId)?.name ?? areaId;
  }
</script>

<div class="head"><h1>{m.requests_title()}</h1></div>
<AuditTabs current="requests" pending={data ? open.length : null} locale={ctx.locale} />

<div class="hm-visually-hidden" aria-live="polite">{spoken}</div>

{#if loader.status === 'error'}
  <ErrorState title={m.overview_error_title()} body={m.overview_error_body()} onretry={() => void loader.run()} />
{:else if data}
  {#if tabs}
    <div class="switch" role="tablist" aria-label={m.requests_title()}>
      {#each TABS as name (name)}
        <button
          type="button"
          role="tab"
          id="{id}-tab-{name}"
          aria-selected={tab === name}
          aria-controls="{id}-{name}"
          tabindex={tab === name ? 0 : -1}
          onclick={() => (tab = name)}
          onkeydown={tabKey}>{TAB_LABELS[name]()}</button
        >
      {/each}
    </div>
  {/if}

  <div class="columns" class:split={desktop.matches}>
    <section
      id="{id}-open"
      bind:this={openSection}
      hidden={hideOpen}
      role={tabs ? 'tabpanel' : undefined}
      aria-labelledby={tabs ? `${id}-tab-open` : `${id}-open-h`}
    >
        <h2 id="{id}-open-h" tabindex="-1" bind:this={openHeading}>{m.requests_tab_open()} <span class="count">{new Intl.NumberFormat(ctx.locale).format(open.length)}</span></h2>
        {#if haDown}<p class="note warning"><Icon name="warning" size={16} />{m.requests_ha_down()}</p>{/if}
        {#if open.length === 0}
          <div class="empty">
            <Icon name="ask" size={32} />
            <strong>{m.requests_empty_title()}</strong>
            <span>{m.requests_empty_body()}</span>
          </div>
        {:else}
          {#each open as request (request.id)}
            <div out:slide={{ duration: leave }} data-request={request.id}>
              <RequestCard {request} areaName={areaName(request.area)} offsetMs={app.offsetMs} {ctx}>
                {#snippet children(titleId)}
                  {#if request.can_answer}
                    <RequestAnswer
                      {request}
                      busy={busy === request.id}
                      describedBy={titleId}
                      onanswer={(approve) => void answer(request.id, approve)}
                    />
                  {/if}
                {/snippet}
              </RequestCard>
            </div>
          {/each}
          <p class="note"><Icon name="info" size={16} />{answerHere ? m.requests_ui_approve_note() : m.request_phone_note()}</p>
        {/if}
    </section>

      <section
        id="{id}-history"
        hidden={hideHistory}
        role={tabs ? 'tabpanel' : undefined}
        aria-labelledby={tabs ? `${id}-tab-history` : `${id}-history-h`}
      >
        <h2 id="{id}-history-h">{m.requests_tab_history()}</h2>
        {#if done.length === 0}
          <div class="empty">
            <Icon name="history" size={32} />
            <strong>{m.requests_history_empty_title()}</strong>
            <span>{m.requests_history_empty_body()}</span>
          </div>
        {:else}
          <ul role="list">
            {#each done as entry (entry.seq)}
              {@const outcome = historyOutcome(entry)}
              <li class:dashed={outcome.dashed}>
                <span class="icon {outcome.tone}"><Icon name={outcome.icon} /></span>
                <div class="text">
                  <span class="result {outcome.tone}">{outcome.text}</span>
                  {#if outcome.hint}<span class="hint">{outcome.hint}</span>{/if}
                  <span class="what"
                    ><AgentName name={entry.agent.display_name} />{SEPARATOR}{cleanUntrusted(entry.device_name)}{SEPARATOR}{actionLabel(undefined, entry.action)}</span
                  >
                  <span class="meta">
                    <time datetime={entry.answered_at}>{formatDateTime(new Date(entry.answered_at), ctx)}</time>
                    <a href={href({ name: 'audit_entry', seq: entry.seq })}>{m.audit_detail_title({ number: entry.seq })}</a>
                  </span>
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
  </div>
{/if}

<style>
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-3xl);
  }
  .switch {
    display: flex;
    gap: var(--hm-space-1);
    padding: var(--hm-space-1);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
  }
  .switch button {
    flex: 1;
    min-block-size: var(--hm-size-touch);
    border: 0;
    border-radius: var(--hm-radius-sm);
    background: transparent;
    color: var(--hm-color-text-muted);
    font: inherit;
    font-weight: var(--hm-font-weight-medium);
  }
  .switch button[aria-selected='true'] {
    background: var(--hm-color-surface);
    color: var(--hm-color-text);
    font-weight: 600;
  }
  @media (forced-colors: active) {
    .switch button {
      border: 1px solid ButtonText;
    }
    .switch button[aria-selected='true'] {
      outline: 2px solid Highlight;
    }
  }
  .switch button:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .columns {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--hm-space-5);
    align-items: start;
  }
  .columns.split {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  }
  section {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    min-inline-size: 0;
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
  }
  h2:focus {
    outline: none;
  }
  section[hidden] {
    display: none;
  }
  .count,
  .note,
  .hint,
  .what,
  .meta {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .note {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
  }
  .warning {
    color: var(--hm-color-warning-fg);
  }
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hm-space-2);
    padding: var(--hm-space-5);
    border: var(--hm-border-width) dashed var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    color: var(--hm-color-text-muted);
    text-align: center;
  }
  .empty strong {
    color: var(--hm-color-text);
  }
  ul {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--hm-space-3);
    padding: var(--hm-space-3);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
  }
  li.dashed {
    border-style: dashed;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-inline-size: 0;
    overflow-wrap: anywhere;
  }
  .result {
    font-weight: 600;
  }
  .positive {
    color: var(--hm-color-positive-fg);
  }
  .danger {
    color: var(--hm-color-danger-fg);
  }
  .warning {
    color: var(--hm-color-warning-fg);
  }
  .muted {
    color: var(--hm-color-text-muted);
  }
  .meta {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
  .meta a {
    color: var(--hm-color-accent-text);
  }
</style>
