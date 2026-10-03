<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing by code (design README 6.2, view=pair; decisions D5, G1, G4): enter the code,
  check the agent, choose its mandate, done. Each step change moves the focus to the new
  step's heading. If the code runs out or gets locked on the way, the person is back at
  step 1 with that state; "This isn't my agent" declines the pairing.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { Agent, DeviceCatalog, PairingCandidate, Template } from '../api/types.ts';
  import { codeError, type CodeState } from '../agents/pairing.ts';
  import BackLink from '../components/BackLink.svelte';
  import ErrorState from '../components/ErrorState.svelte';
  import Icon from '../components/Icon.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import PairCode from '../components/agents/PairCode.svelte';
  import PairMandate from '../components/agents/PairMandate.svelte';
  import PairSteps from '../components/agents/PairSteps.svelte';
  import PairVerify from '../components/agents/PairVerify.svelte';
  import { m } from '../i18n.ts';
  import { templateName } from '../mandate/template.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';
  import { MARK, around } from '../ui/sentence.ts';
  import { toasts } from '../ui/toasts.ts';
  import { cleanUntrusted, isolate } from '../untrusted.ts';

  interface Props {
    app: AppState;
    /** Browser clock, ticking. */
    now: number;
  }

  interface Choices {
    templates: Template[];
    catalog: DeviceCatalog;
  }

  type Step = 'code' | 'verify' | 'mandate' | 'done';

  let { app, now }: Props = $props();

  const NO_CATALOG: DeviceCatalog = { areas: [], devices: [] };
  const STEP_NUMBER: Record<Exclude<Step, 'done'>, number> = { code: 1, verify: 2, mandate: 3 };
  const SEPARATOR = ' · ';

  const id = $props.id();
  const headingId = `${id}-step`;

  let step: Step = $state('code');
  let code = $state('');
  let codeState: CodeState = $state('idle');
  let candidate: PairingCandidate | null = $state.raw(null);
  let admitted: Agent | null = $state.raw(null);
  let mandateName = $state('');
  let busy = $state(false);
  let error = $state('');
  /** Remounts the code step, so it starts from the given code and state. */
  let round = $state(0);

  const choices = new Loader<Choices>(async () => {
    const api = app.api;
    const [names, catalog] = await Promise.all([api.templates(), api.devices().catch(() => NO_CATALOG)]);
    const templates = await Promise.all(names.map((t) => api.template(t.name)));
    return { templates, catalog };
  });

  onMount(() => void choices.run());

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });

  async function go(next: Step) {
    step = next;
    error = '';
    await tick();
    document.getElementById(headingId)?.focus();
  }

  function found(checked: string, c: PairingCandidate) {
    code = checked;
    candidate = c;
    void go('verify');
  }

  /** restart goes back to step 1, keeping the code and showing why. */
  function restart(state: CodeState) {
    codeState = state;
    candidate = null;
    round++;
    void go('code');
  }

  async function notMine() {
    if (busy) return;
    busy = true;
    try {
      await app.api.pairingDeny(code);
      toasts.show({ kind: 'success', text: m.pair_denied_toast() });
      code = '';
      restart('idle');
    } catch (err) {
      const result = codeError(err);
      if (result.state === 'failed') toasts.show({ kind: 'error', text: m.pair_failed() });
      else restart(result.state);
    } finally {
      busy = false;
    }
  }

  async function confirm(template: string, displayName: string) {
    if (busy) return;
    busy = true;
    error = '';
    const name = templateName(template);
    try {
      admitted = await app.api.pairingApprove({ code, display_name: displayName, template, mandate_name: name });
      mandateName = admitted.mandate?.name ?? name;
      await go('done');
    } catch (err) {
      const result = codeError(err);
      if (result.state === 'failed') error = m.pair_failed();
      else restart(result.state);
    } finally {
      busy = false;
    }
  }

  const doneTitle = $derived(around(m.pair_done_title({ agent: MARK })));
</script>

<BackLink href={href({ name: 'agents' })}>{m.agents_title()}</BackLink>
<PageHeader title="{m.agents_add()}{SEPARATOR}{m.agents_way_code_title()}" />

<div class="flow">
  {#if step !== 'done'}<PairSteps current={STEP_NUMBER[step]} />{/if}

  {#if step === 'code'}
    {#key round}
      <PairCode api={app.api} {now} initial={code} initialState={codeState} {headingId} onfound={found} />
    {/key}
  {:else if step === 'verify' && candidate}
    <PairVerify
      {code}
      {candidate}
      {ctx}
      serverNow={now - app.offsetMs}
      {headingId}
      {busy}
      onnotmine={notMine}
      oncontinue={() => void go('mandate')}
    />
  {:else if step === 'mandate' && candidate}
    {#if choices.status === 'error'}
      <ErrorState title={m.mandates_error_title()} body={m.pair_failed()} onretry={() => void choices.run()} />
    {:else if choices.data}
      <PairMandate
        templates={choices.data.templates}
        catalog={choices.data.catalog}
        locale={ctx.locale}
        claimedName={candidate.claimed_name}
        {headingId}
        {busy}
        {error}
        onback={() => void go('verify')}
        onconfirm={confirm}
      />
    {:else}
      <p role="status">{m.common_loading()}</p>
    {/if}
  {:else if step === 'done' && admitted}
    <section class="done" aria-labelledby={headingId}>
      <span class="icon"><Icon name="check" size={32} /></span>
      <h2 id={headingId} tabindex="-1">{doneTitle[0]}<bdi>{cleanUntrusted(admitted.display_name)}</bdi>{doneTitle[1]}</h2>
      <p>{m.pair_done_body({ mandate: isolate(mandateName) })}</p>
      <div class="links">
        <a class="primary" href={href({ name: 'agent', id: admitted.client_id })}>{m.pair_done_open()}</a>
        {#if admitted.mandate}<a href={href({ name: 'mandate', id: admitted.mandate.id })}>{m.pair_done_edit()}</a>{/if}
      </div>
    </section>
  {/if}
</div>

<style>
  .flow {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-5);
    inline-size: 100%;
    max-inline-size: 680px;
  }
  .done {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-3);
    padding: var(--hm-space-6);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .icon {
    display: flex;
    padding: 12px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-positive-bg);
    color: var(--hm-color-positive-fg);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  h2:focus {
    outline: none;
  }
  p {
    margin: 0;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .links {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
  .links a {
    display: inline-flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    color: var(--hm-color-text);
    font-weight: var(--hm-font-weight-semibold);
    text-decoration: none;
  }
  .links a.primary {
    background: var(--hm-color-accent);
    border-color: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .links a:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
</style>
