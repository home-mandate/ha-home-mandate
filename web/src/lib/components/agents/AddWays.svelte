<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The two ways an agent signs in (design README 6.2): pairing code or browser sign-in, as link
  cards. The only place that links to a way directly: every other entry point leads here.
  The heading can take the focus when the ways open on arrival.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import { href } from '../../router.ts';
  import Icon, { type IconName } from '../Icon.svelte';

  interface Props {
    /** Id for the heading, so the section can be named by it. */
    headingId: string;
  }

  let { headingId }: Props = $props();

  const ways: { href: string; icon: IconName; title: () => string; desc: () => string }[] = [
    { href: href({ name: 'pair' }), icon: 'dots', title: () => m.agents_way_code_title(), desc: () => m.agents_way_code_desc() },
    { href: href({ name: 'connect' }), icon: 'link', title: () => m.agents_way_browser_title(), desc: () => m.agents_way_browser_desc() },
  ];
</script>

<section aria-labelledby={headingId}>
  <h2 id={headingId} tabindex="-1">{m.agents_add_how()}</h2>
  <ul role="list">
    {#each ways as way (way.href)}
      <li>
        <a href={way.href}>
          <span class="icon"><Icon name={way.icon} size={20} /></span>
          <span class="text"><span class="title">{way.title()}</span><span class="desc">{way.desc()}</span></span>
          <span class="chevron"><Icon name="chevron" /></span>
        </a>
      </li>
    {/each}
  </ul>
</section>

<style>
  section {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  h2 {
    margin: 0;
    font-size: 20px;
    font-weight: var(--hm-font-weight-semibold);
  }
  ul {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 300px), 1fr));
    gap: var(--hm-space-3);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  a {
    display: flex;
    gap: var(--hm-space-4);
    align-items: flex-start;
    block-size: 100%;
    box-sizing: border-box;
    padding: var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    color: var(--hm-color-text);
    text-decoration: none;
  }
  a:hover {
    border-color: var(--hm-color-border-strong);
  }
  a:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .icon {
    display: flex;
    padding: 10px;
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-accent-text);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    flex: 1;
    min-inline-size: 0;
  }
  .title {
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  .desc {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .chevron {
    display: flex;
    color: var(--hm-color-text-subtle);
  }
</style>
