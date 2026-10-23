<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One approval request on its own page (#/audit/requests/:id, issue #53): the target of
  every notification. Its approvers see what is to be confirmed and, if they may answer
  here, decline and approve as large buttons; once it is answered or has ended everyone
  sees only the outcome; anyone else, and an ID that is unknown or gone, see the same
  neutral sentence. The page follows the request live.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { ApprovalPage, DeviceCatalog } from '../api/types.ts';
  import { answerError } from '../approvals/answer.ts';
  import { pageOutcome } from '../approvals/history.ts';
  import BackLink from '../components/BackLink.svelte';
  import ClaimLegend from '../components/ClaimLegend.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import RequestAnswer from '../components/RequestAnswer.svelte';
  import RequestCard from '../components/RequestCard.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { m } from '../i18n.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { Announcer } from '../ui/announcer.svelte.ts';
  import { toasts } from '../ui/toasts.ts';

  interface Props {
    app: AppState;
    id: string;
  }

  interface Data {
    page: ApprovalPage;
    catalog: DeviceCatalog | null;
  }

  let { app, id }: Props = $props();

  const SKELETON = ['40%', '70%', '60%'];

  let busy = $state(false);
  const live = new Announcer();

  const loader = new Loader<Data>(async () => {
    const [page, catalog] = await Promise.all([app.api.approval(id), app.api.devices().catch(() => null)]);
    return { page, catalog };
  });

  /** reload fetches the page again and says the outcome once the request has ended. */
  async function reload() {
    const wasOpen = loader.data?.page.status === 'open';
    await loader.run();
    const page = loader.data?.page;
    if (wasOpen && page?.status === 'closed') await live.say(pageOutcome(page.outcome, page.cause).text);
  }

  onMount(() => {
    const stop = [
      app.on('approval.opened', ({ request }) => {
        if (request.id === id) void reload();
      }),
      app.on('approval.closed', (event) => {
        if (event.id === id) void reload();
      }),
      app.on('reconnected', () => void reload()),
    ];
    void loader.run();
    return () => stop.forEach((off) => off());
  });

  async function answer(approve: boolean) {
    if (busy) return;
    busy = true;
    try {
      await app.api.answerApproval(id, approve);
    } catch (err) {
      toasts.show({ kind: 'error', text: answerError(err) });
    }
    // The buttons stay busy until the page shows the outcome, so it offers no second
    // answer meanwhile (security review S8).
    try {
      await reload();
    } finally {
      busy = false;
    }
  }

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const page = $derived(loader.data?.page ?? null);

  function areaName(areaId: string | null): string | null {
    if (areaId === null) return null;
    return loader.data?.catalog?.areas.find((a) => a.id === areaId)?.name ?? areaId;
  }
</script>

<BackLink href={href({ name: 'requests' })} label={m.request_page_all()} />
<h1>{m.request_page_title()}</h1>

<div class="hm-visually-hidden" aria-live="polite">{live.text}</div>

{#if loader.status === 'error'}
  <ErrorState title={m.overview_error_title()} body={m.overview_error_body()} onretry={() => void loader.run()} />
{:else if !page}
  <Skeleton lines={SKELETON} />
{:else if page.status === 'open'}
  {@const request = page.request}
  <RequestCard {request} areaName={areaName(request.area)} offsetMs={app.offsetMs} {ctx}>
    {#snippet children(titleId)}
      {#if request.can_answer}
        <RequestAnswer {request} {busy} describedBy={titleId} large onanswer={(approve) => void answer(approve)} />
      {/if}
    {/snippet}
  </RequestCard>
  <p class="note"><Icon name="info" size={16} />{request.can_answer ? m.requests_ui_approve_note() : m.request_phone_note()}</p>
  <ClaimLegend />
{:else if page.status === 'closed'}
  {@const outcome = pageOutcome(page.outcome, page.cause)}
  <section class="state" class:dashed={outcome.dashed} aria-label={m.request_page_closed()}>
    <span class="icon {outcome.tone}"><Icon name={outcome.icon} size={32} /></span>
    <strong class={outcome.tone}>{outcome.text}</strong>
    {#if outcome.hint}<span class="hint">{outcome.hint}</span>{/if}
    <span>{m.request_page_closed()}</span>
  </section>
{:else}
  <section class="state dashed">
    <span class="icon muted"><Icon name="ask" size={32} /></span>
    <span>{m.request_page_unavailable()}</span>
  </section>
{/if}

<style>
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-3xl);
  }
  .note,
  .hint {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  .note {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
  }
  .state {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hm-space-2);
    padding: var(--hm-space-5);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    text-align: center;
    overflow-wrap: anywhere;
  }
  .state.dashed {
    border-style: dashed;
  }
  strong {
    font-size: var(--hm-font-size-xl);
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
</style>
