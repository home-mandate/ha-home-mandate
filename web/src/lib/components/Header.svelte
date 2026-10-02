<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Global header (design README section 5): logo and name, the five sections, and the
  emergency stop, always visible. Desktop: one 56 px row. Below 768 px: logo and emergency
  stop in the first row, the sections as a scrolling tab bar in the second.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import type { Section } from '../router.ts';
  import Icon from './Icon.svelte';

  interface Props {
    section: Section | null;
    estopActive: boolean;
    /** False without access: name only, no sections, no emergency stop. */
    showNav?: boolean;
    onestop: () => void;
  }

  let { section, estopActive, showNav = true, onestop }: Props = $props();

  const TABS: readonly { key: Section; href: string; label: () => string }[] = [
    { key: 'overview', href: '#/', label: () => m.nav_overview() },
    { key: 'agents', href: '#/agents', label: () => m.nav_agents() },
    { key: 'mandates', href: '#/mandates', label: () => m.nav_mandates() },
    { key: 'audit', href: '#/audit', label: () => m.nav_audit() },
    { key: 'settings', href: '#/settings', label: () => m.nav_settings() },
  ];
</script>

<header class="header">
  <span class="brand">
    <img src="./hm-logo-64.png" srcset="./hm-logo-64.png 1x, ./hm-logo-128.png 2x" alt="" width="36" height="36" />
    <span class="name">{m.app_name()}</span>
  </span>
  {#if showNav}
    <nav aria-label={m.nav_label()}>
      {#each TABS as tab (tab.key)}
        <a href={tab.href} aria-current={section === tab.key ? 'page' : undefined}>{tab.label()}</a>
      {/each}
    </nav>
    <button type="button" class="estop" class:active={estopActive} onclick={onestop}>
      <Icon name="power" />
      <span>{estopActive ? m.estop_button_active() : m.estop_button()}</span>
    </button>
  {/if}
</header>

<style>
  .header {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0 var(--hm-space-6);
    padding-inline: var(--hm-space-8);
    min-block-size: 56px;
    background: var(--hm-color-surface);
    border-block-end: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-shrink: 0;
    min-inline-size: 0;
  }
  img {
    display: block;
    inline-size: 36px;
    block-size: 36px;
    flex-shrink: 0;
  }
  .name {
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  nav {
    order: 2;
    display: flex;
    gap: var(--hm-space-1);
    flex: 1;
    min-inline-size: 0;
    align-self: stretch;
    flex-wrap: wrap;
  }
  a {
    display: flex;
    align-items: center;
    padding-inline: var(--hm-space-3);
    min-block-size: 56px;
    box-sizing: border-box;
    font-size: 15px;
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text-muted);
    text-decoration: none;
    border-block-end: 2px solid transparent;
  }
  a:hover {
    color: var(--hm-color-text);
    background: var(--hm-color-surface-hover);
  }
  a[aria-current='page'] {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
    border-block-end-color: var(--hm-color-accent);
  }
  .estop {
    order: 3;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-control);
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    white-space: nowrap;
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-danger-fg);
    cursor: pointer;
  }
  .estop.active {
    color: var(--hm-color-on-danger);
    background: var(--hm-color-danger-solid);
    border-color: var(--hm-color-danger-solid);
  }
  @media (max-width: 767px) {
    .header {
      justify-content: space-between;
      gap: var(--hm-space-3);
      padding-block: var(--hm-space-1) 0;
      padding-inline: var(--hm-space-4) var(--hm-space-2);
    }
    .brand {
      gap: var(--hm-space-2);
      min-block-size: 52px;
    }
    nav {
      order: 4;
      flex: 1 0 100%;
      flex-wrap: nowrap;
      gap: 2px;
      overflow-x: auto;
      margin-inline: calc(-1 * var(--hm-space-4)) calc(-1 * var(--hm-space-2));
      padding-inline: var(--hm-space-2);
      scrollbar-width: none;
    }
    a {
      flex-shrink: 0;
      min-block-size: var(--hm-size-touch);
      white-space: nowrap;
    }
    .estop {
      min-block-size: var(--hm-size-touch);
      padding-inline: var(--hm-space-3);
      gap: 6px;
      font-size: var(--hm-font-size-sm);
    }
  }
  @media (forced-colors: active) {
    a[aria-current='page'] {
      border-block-end-color: Highlight;
    }
  }
</style>
