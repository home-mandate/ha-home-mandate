<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Says once per list what the dotted underline of agent names means (decision L1): the
  name is the agent's own claim, not verified. A title explains it only to a mouse; screen
  readers hear ", unverified" at every name, so this line is for the eyes only.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { MARK, around } from '../ui/sentence.ts';

  const [before, after] = around(m.agent_claim_legend({ sample: MARK }));
</script>

<p class="legend" aria-hidden="true">{before}<span class="sample">{m.agent_claim_legend_sample()}</span>{after}</p>

<style>
  .legend {
    margin: 0;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .sample {
    text-decoration: underline dotted var(--hm-color-text-subtle);
    text-underline-offset: 4px;
  }
</style>
