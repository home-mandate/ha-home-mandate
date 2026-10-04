<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Decision badge: never colour alone. Icon shape + word + border style; "default" (no rule
  allows it) has a dashed border and its own icon, "deny" (a rule forbids it) a solid one.
  "critical" is a marker for critical actions, not a decision.
-->
<script lang="ts" module>
  export type BadgeKind = 'allow' | 'ask' | 'deny' | 'default' | 'critical';
</script>

<script lang="ts">
  import { m } from '../i18n.ts';
  import Icon from './Icon.svelte';

  interface Props {
    kind: BadgeKind;
    size?: 'md' | 'sm';
  }

  let { kind, size = 'md' }: Props = $props();

  const LABELS: Record<BadgeKind, () => string> = {
    allow: () => m.decision_allow(),
    ask: () => m.decision_ask(),
    deny: () => m.decision_deny(),
    default: () => m.decision_default(),
    critical: () => m.critical_label(),
  };
</script>

<span class="badge {kind} {size}"><Icon name={kind} size={16} /><span>{LABELS[kind]()}</span></span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding-block: 1px;
    padding-inline: 6px 9px;
    border-radius: var(--hm-radius-pill);
    border: var(--hm-border-width) solid;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    line-height: var(--hm-line-height-normal);
    white-space: nowrap;
  }
  .sm {
    gap: var(--hm-space-1);
    padding-block: 0;
    padding-inline: 5px var(--hm-space-2);
    font-size: var(--hm-font-size-xs);
  }
  .allow {
    color: var(--hm-color-allow-fg);
    background: var(--hm-color-allow-bg);
    border-color: var(--hm-color-allow-border);
  }
  .ask {
    color: var(--hm-color-ask-fg);
    background: var(--hm-color-ask-bg);
    border-color: var(--hm-color-ask-border);
  }
  .deny {
    color: var(--hm-color-deny-fg);
    background: var(--hm-color-deny-bg);
    border-color: var(--hm-color-deny-border);
  }
  .default {
    color: var(--hm-color-default-fg);
    background: var(--hm-color-default-bg);
    border-color: var(--hm-color-default-border);
    border-style: dashed;
  }
  .critical {
    color: var(--hm-color-critical-fg);
    background: var(--hm-color-critical-bg);
    border-color: var(--hm-color-critical-border);
  }
</style>
