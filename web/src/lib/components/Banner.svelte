<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Full-width banner under the header (design README section 5). info is a status; warning,
  critical and the emergency stop (estop, solid) are alerts. Order when several show:
  emergency stop › chain broken › HA disconnected › mandate invalid › TLS missing.
-->
<script lang="ts">
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    kind: 'info' | 'warning' | 'critical' | 'estop';
    title?: string;
    body: string;
    action?: { label: string; onclick: () => void };
  }

  let { kind, title, body, action }: Props = $props();

  const ICONS: Record<Props['kind'], IconName> = { info: 'info', warning: 'warning', critical: 'warning', estop: 'power' };
</script>

<div class="banner {kind}" role={kind === 'info' ? 'status' : 'alert'}>
  <span class="icon"><Icon name={ICONS[kind]} /></span>
  <div class="text">
    {#if title}<strong>{title}</strong>{/if}
    <span>{body}</span>
  </div>
  {#if action}<button type="button" onclick={action.onclick}>{action.label}</button>{/if}
</div>

<style>
  .banner {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-4);
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: 10px;
    border: var(--hm-border-width) solid;
  }
  .icon {
    display: flex;
    align-self: flex-start;
    padding-block-start: 2px;
  }
  .text {
    flex: 1 1 300px;
    min-inline-size: 0;
    font-size: 15px;
    overflow-wrap: anywhere;
  }
  strong {
    font-weight: var(--hm-font-weight-semibold);
  }
  .text span {
    display: block;
  }
  button {
    min-block-size: var(--hm-size-control);
    padding-inline: 14px;
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    background: transparent;
    color: inherit;
    border: var(--hm-border-width) solid currentColor;
    cursor: pointer;
  }
  .info {
    background: var(--hm-color-info-bg);
    color: var(--hm-color-info-fg);
    border-color: var(--hm-color-info-border);
  }
  .warning {
    background: var(--hm-color-warning-bg);
    color: var(--hm-color-warning-fg);
    border-color: var(--hm-color-warning-border);
  }
  .critical {
    background: var(--hm-color-danger-bg);
    color: var(--hm-color-danger-fg);
    border-color: var(--hm-color-danger-border);
  }
  .estop {
    background: var(--hm-color-danger-solid);
    color: var(--hm-color-on-danger);
    border-color: var(--hm-color-danger-solid);
  }
</style>
