<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Sub-navigation of the audit log (design README 6.8): events and approvals, the latter
  with the number of pending requests. Links, so the browser history and bookmarks work.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import { href } from '../../router.ts';

  interface Props {
    current: 'events' | 'requests';
    /** Pending approvals; null while unknown. */
    pending: number | null;
    locale: string;
  }

  let { current, pending, locale }: Props = $props();

  const badge = $derived(pending ? new Intl.NumberFormat(locale).format(pending) : '');
</script>

<nav aria-label={m.audit_title()}>
  <a href={href({ name: 'audit', query: {} })} aria-current={current === 'events' ? 'page' : undefined}>{m.audit_tab_events()}</a>
  <a href={href({ name: 'requests' })} aria-current={current === 'requests' ? 'page' : undefined}
    >{m.audit_tab_requests()}{#if badge}<span class="badge" aria-hidden="true">{badge}</span><span class="hm-visually-hidden"
          >{m.audit_tab_pending_count({ count: badge })}</span
        >{/if}</a
  >
</nav>

<style>
  nav {
    display: flex;
    gap: var(--hm-space-4);
    border-block-end: var(--hm-border-width) solid var(--hm-color-border-subtle);
    overflow-x: auto;
    /* room for the focus ring inside the scroll container */
    padding-block-start: 4px;
  }
  a {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-1);
    border-block-end: 3px solid transparent;
    color: var(--hm-color-text-muted);
    font-weight: var(--hm-font-weight-medium);
    text-decoration: none;
    white-space: nowrap;
  }
  a[aria-current='page'] {
    color: var(--hm-color-accent-text);
    border-block-end-color: var(--hm-color-accent);
    font-weight: 600;
  }
  @media (forced-colors: active) {
    a[aria-current='page'] {
      border-block-end-color: Highlight;
    }
  }
  a:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .badge {
    min-inline-size: 20px;
    padding-inline: 6px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-ask-bg);
    color: var(--hm-color-ask-fg);
    font-size: var(--hm-font-size-xs);
    text-align: center;
  }
</style>
