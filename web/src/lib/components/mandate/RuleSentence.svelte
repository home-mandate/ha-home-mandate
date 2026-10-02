<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  A rule as a sentence: "Lights · Kitchen: turn on, turn off → Allowed". Device and area
  names come from Home Assistant and are isolated; the decision is always icon and word.
  A rule that allows critical actions without approval never reads as a plain "Allowed".
-->
<script lang="ts">
  import type { Decision } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import type { RuleText } from '../../mandate/text.ts';
  import DecisionBadge from '../DecisionBadge.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    text: RuleText;
    decision: Decision;
    /** sm for the save summary and the version compare. */
    size?: 'md' | 'sm';
    /** The rule carries allow_critical; shown unless the caller says it in a note of its own. */
    critical?: boolean;
  }

  let { text, decision, size = 'md', critical = false }: Props = $props();

  // Separators as values: whitespace at the edge of an element would be trimmed.
  const DOT = ' · ';
  const COLON = ': ';
  const ARROW = ' → ';
</script>

<span class="sentence {size}">
  <strong><bdi>{text.subject}</bdi></strong>{#if text.area}<span class="muted">{DOT}<bdi>{text.area}</bdi></span>{/if}<span class="muted">{COLON}</span><strong
    >{text.actions}</strong
  ><span class="arrow" aria-hidden="true">{ARROW}</span><span class="hm-visually-hidden">{COLON}</span><DecisionBadge kind={decision} size={size === 'sm' ? 'sm' : 'md'} />
  {#if critical}<span class="critical"><Icon name="warning" size={16} /><span>{m.critical_override_active()}</span></span>{/if}
</span>

<style>
  .sentence {
    font-size: var(--hm-font-size-md);
    line-height: var(--hm-line-height-normal);
    overflow-wrap: anywhere;
    text-wrap: pretty;
  }
  .sm {
    font-size: var(--hm-font-size-sm);
  }
  strong {
    font-weight: var(--hm-font-weight-semibold);
  }
  .muted {
    color: var(--hm-color-text-muted);
  }
  .arrow {
    color: var(--hm-color-text-subtle);
  }
  .sentence :global(.badge) {
    vertical-align: -4px;
  }
  .critical {
    display: flex;
    gap: 6px;
    margin-block-start: var(--hm-space-1);
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
  }
  .critical :global(svg) {
    flex-shrink: 0;
    margin-block-start: 2px;
  }
</style>
