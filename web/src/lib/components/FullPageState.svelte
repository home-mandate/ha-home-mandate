<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Whole-page states inside <main>: no access, not found (design "Frame & Overview"). -->
<script lang="ts">
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    icon: IconName;
    badge: IconName;
    title: string;
    body: string;
    note?: string;
    /** A link; its icon is "back" unless named. */
    cta?: { href: string; label: string; icon?: IconName };
  }

  let { icon, badge, title, body, note, cta }: Props = $props();
</script>

<section class="state">
  <span class="circle">
    <Icon name={icon} size={32} />
    <span class="badge"><Icon name={badge} size={16} /></span>
  </span>
  <h1>{title}</h1>
  <p>{body}</p>
  {#if note}<p class="note"><Icon name="info" />{note}</p>{/if}
  {#if cta}<a href={cta.href}><Icon name={cta.icon ?? 'back'} />{cta.label}</a>{/if}
</section>

<style>
  .state {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 14px;
    padding: var(--hm-space-16) var(--hm-space-4);
  }
  .circle {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 112px;
    block-size: 112px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-muted);
  }
  .badge {
    position: absolute;
    inset-block-start: 6px;
    inset-inline-end: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 36px;
    block-size: 36px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border);
    color: var(--hm-color-accent-text);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
    max-inline-size: 480px;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text-muted);
  }
  .note {
    display: flex;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-info-bg);
    color: var(--hm-color-info-fg);
    font-size: 15px;
    text-align: start;
  }
  a {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    padding-inline: 18px;
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    text-decoration: none;
  }
</style>
