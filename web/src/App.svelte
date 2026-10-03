<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The frame of the UI (design README section 5): skip link, header, banners, the page of the
  current route, toasts and the emergency stop sheet. Data comes from AppState; a load error
  says what keeps working, and a missing admin right shows "no access" without navigation.
-->
<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { BrowserNotifier } from './lib/app/notifier.svelte.ts';
  import type { AppState } from './lib/app/state.svelte.ts';
  import BannerStack from './lib/components/BannerStack.svelte';
  import ErrorState from './lib/components/ErrorState.svelte';
  import EstopSheet from './lib/components/EstopSheet.svelte';
  import FullPageState from './lib/components/FullPageState.svelte';
  import Header from './lib/components/Header.svelte';
  import Skeleton from './lib/components/Skeleton.svelte';
  import ToastHost from './lib/components/ToastHost.svelte';
  import { m } from './lib/i18n.ts';
  import MandateEditor from './lib/pages/MandateEditor.svelte';
  import MandateList from './lib/pages/MandateList.svelte';
  import AuditEntry from './lib/pages/AuditEntry.svelte';
  import AuditLog from './lib/pages/AuditLog.svelte';
  import MandateVersions from './lib/pages/MandateVersions.svelte';
  import Overview from './lib/pages/Overview.svelte';
  import Requests from './lib/pages/Requests.svelte';
  import Settings from './lib/pages/Settings.svelte';
  import AgentConnect from './lib/pages/AgentConnect.svelte';
  import AgentDetail from './lib/pages/AgentDetail.svelte';
  import AgentPair from './lib/pages/AgentPair.svelte';
  import Agents from './lib/pages/Agents.svelte';
  import { getLocale } from './lib/paraglide/runtime.js';
  import { href, parseHash, sectionOf, type Route, type Section } from './lib/router.ts';
  import { toasts } from './lib/ui/toasts.ts';

  interface Props {
    app: AppState;
  }

  let { app }: Props = $props();

  const TICK_MS = 1000;

  let route: Route = $state(parseHash(window.location.hash));
  /** Counts navigations: pages that keep their state in the URL themselves (audit
   *  filters via replaceState) are rebuilt on every real navigation, also to an equal URL. */
  let visits = $state(0);
  let now = $state(Date.now());
  let sheet = $state(false);
  let firing = $state(false);
  let estopError = $state('');
  let main: HTMLElement | undefined = $state();

  /** localStorage, or none where the browser blocks it (private mode, sandboxed frame). */
  function storage(): Storage | undefined {
    try {
      return window.localStorage;
    } catch {
      return undefined;
    }
  }

  // Browser notifications for new requests (decision S4), on every page.
  const notifier = new BrowserNotifier({
    Notification: typeof Notification === 'undefined' ? undefined : Notification,
    storage: storage(),
    hidden: () => document.hidden,
  });
  onMount(() =>
    app.on('approval.opened', ({ request }) =>
      notifier.notify(request, { title: m.notify_title(), body: m.notify_body() }, () => {
        window.focus();
        go({ name: 'requests' });
      }),
    ),
  );

  const timer = setInterval(() => (now = Date.now()), TICK_MS);
  onDestroy(() => {
    clearInterval(timer);
    app.stop();
  });

  const TITLES: Record<Section, () => string> = {
    overview: () => m.overview_title(),
    agents: () => m.agents_title(),
    mandates: () => m.mandates_title(),
    audit: () => m.audit_title(),
    settings: () => m.settings_title(),
  };

  /** Navigation: focus moves to the content so screen readers hear the change. */
  function navigated() {
    route = parseHash(window.location.hash);
    visits++;
    // The way back from an audit entry only holds while staying in the audit log.
    if (route.name !== 'audit' && route.name !== 'audit_entry') app.auditReturn = null;
    // Never pull focus out of the open emergency stop sheet (it would cancel a running hold).
    if (!sheet) void focusPage();
  }

  /** focusPage puts the focus on the page's heading, so screen readers hear the page name (review a11y L2). */
  async function focusPage() {
    await tick();
    if (sheet) return;
    const heading = main?.querySelector<HTMLElement>('h1');
    if (!heading) {
      main?.focus();
      return;
    }
    if (!heading.hasAttribute('tabindex')) heading.setAttribute('tabindex', '-1');
    heading.focus();
  }

  const section = $derived(sectionOf(route));
  // The page title follows the route, from the first load on (review a11y M6).
  $effect(() => {
    const page = section ? TITLES[section]() : m.notfound_title();
    document.title = `${page} – ${m.app_name()}`;
  });
  const estopActive = $derived(app.system?.emergency_stop.active ?? false);
  const timeZone = $derived(app.session?.household.time_zone ?? 'UTC');

  function go(target: Route) {
    window.location.hash = href(target);
  }

  function openSheet() {
    estopError = '';
    sheet = true;
  }

  function estop() {
    if (estopActive) {
      go({ name: 'settings', section: 'estop' });
      return;
    }
    openSheet();
  }

  async function fire() {
    if (firing) return;
    firing = true;
    estopError = '';
    try {
      await app.setEmergencyStop(true);
    } catch {
      estopError = m.estop_failed();
      return;
    } finally {
      firing = false;
    }
    sheet = false;
    toasts.show({ kind: 'success', text: m.estop_triggered_toast() });
  }

  function skip(event: MouseEvent) {
    event.preventDefault();
    main?.focus();
  }
</script>

<svelte:window onhashchange={navigated} />

<a class="skip" href="#main" onclick={skip}>{m.skip_to_content()}</a>
<Header {section} {estopActive} showNav={app.phase !== 'forbidden'} onestop={estop} />
{#if app.system && app.phase === 'ready'}
  <BannerStack
    system={app.system}
    downSince={app.downSince}
    {now}
    locale={getLocale()}
    {timeZone}
    onliftestop={() => go({ name: 'settings', section: 'estop' })}
    onchain={(seq) => go({ name: 'audit_entry', seq })}
  />
{/if}
<main id="main" bind:this={main} tabindex="-1" aria-busy={app.phase === 'loading'}>
  {#if app.phase === 'loading'}
    <Skeleton lines={['30%', '70%', '50%']} />
  {:else if app.phase === 'forbidden'}
    <FullPageState icon="lock" badge="person" title={m.noaccess_title()} body={m.noaccess_body()} note={m.noaccess_approver()} />
  {:else if app.phase === 'error'}
    <ErrorState title={m.app_error_title()} body={m.overview_error_body()} onretry={() => window.location.reload()} />
  {:else if route.name === 'not_found' || section === null}
    <FullPageState
      icon="search"
      badge="info"
      title={m.notfound_title()}
      body={m.notfound_body()}
      cta={{ href: '#/', label: m.notfound_back() }}
    />
  {:else if route.name === 'overview'}
    <Overview {app} {now} />
  {:else if route.name === 'audit'}
    {#key visits}<AuditLog {app} {now} query={route.query} />{/key}
  {:else if route.name === 'requests'}
    <Requests {app} />
  {:else if route.name === 'audit_entry'}
    {#key route.seq}<AuditEntry {app} seq={route.seq} />{/key}
  {:else if route.name === 'agents'}
    <Agents {app} {now} />
  {:else if route.name === 'pair'}
    <AgentPair {app} {now} />
  {:else if route.name === 'agent'}
    {#key route.id}<AgentDetail {app} id={route.id} {now} />{/key}
  {:else if route.name === 'connect'}
    <AgentConnect {app} />
  {:else if route.name === 'settings'}
    <Settings {app} {notifier} section={route.section} onestop={openSheet} storage={storage()} />
  {:else if route.name === 'mandates'}
    <MandateList {app} {now} />
  {:else if route.name === 'mandate'}
    {#key route.id}<MandateEditor {app} id={route.id} {now} />{/key}
  {:else if route.name === 'mandate_versions'}
    {#key route.id}<MandateVersions {app} id={route.id} />{/key}
  {/if}
</main>
<ToastHost />
<EstopSheet open={sheet} onclose={() => (sheet = false)} onfire={fire} error={estopError} />

<style>
  main {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-6);
    flex: 1;
    inline-size: 100%;
    max-inline-size: var(--hm-content-max);
    box-sizing: border-box;
    padding-block: var(--hm-space-6) var(--hm-space-10);
    padding-inline: var(--hm-page-pad);
  }
  main:focus {
    outline: none;
  }
  .skip {
    position: absolute;
    inset-inline-start: var(--hm-space-2);
    inset-block-start: -100px;
    z-index: 200;
    padding: var(--hm-space-2) var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .skip:focus {
    inset-block-start: var(--hm-space-2);
  }
</style>
