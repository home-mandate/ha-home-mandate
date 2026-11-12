<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Says once per list, in words for people who know nothing about tokens, what the two
  binding badges mean (issue #156). Shown only where an active agent carries a badge.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
</script>

<div class="legend" data-testid="binding-legend">
  <p><strong>{m.agent_binding_legend_dpop_label()}</strong> {m.agent_binding_legend_dpop()}</p>
  <p><strong>{m.agent_binding_legend_none_label()}</strong> {m.agent_binding_legend_none()}</p>
</div>

<style>
  .legend {
    display: grid;
    gap: var(--hm-space-1);
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  p {
    margin: 0;
  }
  strong {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text);
  }
</style>
