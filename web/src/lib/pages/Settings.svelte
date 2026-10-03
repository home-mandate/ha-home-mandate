<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Settings (design README 6.11; decisions F2, F5, S1–S8): one page with a section index,
  a sticky column on desktop and a scrolling chip row on mobile. #/settings/<section>
  scrolls to that section and moves the focus to its heading. Simple fields save at once;
  security-relevant actions have their own buttons.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Defaults, Language } from '../api/types.ts';
  import { adoptLanguage } from '../app/language.ts';
  import { Loader } from '../app/loader.svelte.ts';
  import type { BrowserNotifier } from '../app/notifier.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import ErrorState from '../components/ErrorState.svelte';
  import PageHeader from '../components/PageHeader.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import AboutSection from '../components/settings/AboutSection.svelte';
  import ApproversSection from '../components/settings/ApproversSection.svelte';
  import DefaultsSection from '../components/settings/DefaultsSection.svelte';
  import EstopSection from '../components/settings/EstopSection.svelte';
  import HaSection from '../components/settings/HaSection.svelte';
  import McpSection from '../components/settings/McpSection.svelte';
  import { m } from '../i18n.ts';
  import { resolveLocale } from '../locale.ts';
  import { baseLocale, getLocale, locales } from '../paraglide/runtime.js';
  import { href, type SettingsSection } from '../router.ts';
  import { toasts } from '../ui/toasts.ts';

  interface Props {
    app: AppState;
    notifier: BrowserNotifier;
    /** Section from the route; null for the top. */
    section: SettingsSection | null;
    /** Opens the emergency stop sheet of the frame. */
    onestop: () => void;
    /** Browser storage for the language setting; none where it is blocked. */
    storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
    reload?: () => void;
  }

  let { app, notifier, section, onestop, storage, reload = () => window.location.reload() }: Props = $props();

  const uid = $props.id();
  const SECTIONS: { key: SettingsSection; title: () => string }[] = [
    { key: 'approvers', title: () => m.set_approvers() },
    { key: 'defaults', title: () => m.set_defaults() },
    { key: 'ha', title: () => m.set_ha() },
    { key: 'mcp', title: () => m.set_mcp() },
    { key: 'retention', title: () => m.set_retention() },
    { key: 'estop', title: () => m.set_estop() },
    { key: 'about', title: () => m.set_about() },
  ];

  const defaults = new Loader<Defaults>(() => app.api.settings());
  onMount(() => {
    const stop = [app.on('settings.changed', () => void defaults.run())];
    void defaults.run();
    return () => stop.forEach((off) => off());
  });

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const browserLanguage = $derived(resolveLocale(undefined, navigator.languages, locales, baseLocale) as Language);

  // The section from the URL: scroll there and give its heading the focus.
  $effect(() => {
    const key = section;
    if (key === null) return;
    void tick().then(() => {
      const heading = document.getElementById(`${uid}-${key}`);
      heading?.scrollIntoView?.({ block: 'start' });
      heading?.focus();
    });
  });

  async function saveDefaults(patch: Partial<Defaults>) {
    const current = defaults.data;
    if (!current) return;
    defaults.set(await app.api.putSettings({ ...current, ...patch }));
  }

  async function bell(on: boolean) {
    try {
      await saveDefaults({ bell: on });
    } catch {
      toasts.show({ kind: 'error', text: m.set_save_failed() });
      void defaults.run();
    }
  }

  /** setLanguage saves the person's setting; Paraglide texts are not reactive, so the page reloads when the language changes. */
  async function setLanguage(language: Language | null) {
    await app.api.setLanguage(language);
    const current = getLocale();
    const next = language ?? browserLanguage;
    const must = storage ? adoptLanguage(language, current, storage) || (language === null && next !== current) : false;
    if (must) reload();
  }
</script>

<PageHeader title={m.settings_title()} />

<div class="layout">
  <nav aria-label={m.settings_nav_label()}>
    <ul role="list">
      {#each SECTIONS as s (s.key)}
        <li><a href={href({ name: 'settings', section: s.key })} aria-current={section === s.key ? 'location' : undefined}>{s.title()}</a></li>
      {/each}
    </ul>
  </nav>

  <div class="sections">
    {#each SECTIONS as s (s.key)}
      <section aria-labelledby="{uid}-{s.key}">
        <h2 id="{uid}-{s.key}" tabindex="-1">{s.title()}</h2>
        {#if s.key === 'approvers'}
          <ApproversSection {app} {notifier} bell={defaults.data?.bell ?? null} onbell={(on) => void bell(on)} />
        {:else if s.key === 'defaults'}
          {#if defaults.status === 'error'}
            <ErrorState title={m.settings_error_title()} body={m.settings_error_body()} onretry={() => void defaults.run()} />
          {:else if defaults.data}
            <DefaultsSection
              defaults={defaults.data}
              language={app.session?.language ?? null}
              {browserLanguage}
              onsave={saveDefaults}
              onlanguage={setLanguage}
            />
          {:else}
            <Skeleton lines={['60%', '40%']} />
          {/if}
        {:else if s.key === 'ha' && app.system}
          <HaSection ha={app.system.ha} {ctx} />
        {:else if s.key === 'mcp' && app.system}
          <McpSection system={app.system} {ctx} />
        {:else if s.key === 'retention'}
          <p class="text">{m.set_retention_value()}</p>
        {:else if s.key === 'estop' && app.system}
          <EstopSection stop={app.system.emergency_stop} {ctx} ontrigger={onestop} onlift={() => app.setEmergencyStop(false)} />
        {:else if s.key === 'about' && app.system}
          <AboutSection version={app.system.version} commit={app.system.commit} />
        {/if}
      </section>
    {/each}
  </div>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--hm-space-5);
  }
  @media (min-width: 768px) {
    .layout {
      grid-template-columns: 200px minmax(0, 1fr);
      align-items: start;
    }
    nav {
      position: sticky;
      inset-block-start: var(--hm-space-4);
    }
    nav ul {
      flex-direction: column;
      overflow: visible;
    }
  }
  nav {
    min-inline-size: 0;
  }
  nav ul {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
    padding: 4px;
    list-style: none;
    overflow-x: auto;
  }
  nav a {
    display: flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    color: var(--hm-color-text);
    text-decoration: none;
    white-space: nowrap;
  }
  nav a:hover {
    background: var(--hm-color-surface-sunken);
  }
  nav a[aria-current='location'] {
    background: var(--hm-color-accent-subtle);
    color: var(--hm-color-accent-text);
    font-weight: var(--hm-font-weight-semibold);
  }
  nav a:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .sections {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-6);
    min-inline-size: 0;
  }
  section {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    padding: var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    scroll-margin-block-start: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
  }
  h2:focus {
    outline: none;
  }
  .text {
    margin: 0;
  }
  @media (forced-colors: active) {
    nav a[aria-current='location'] {
      outline: 2px solid Highlight;
    }
  }
</style>
