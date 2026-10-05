<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  A template in plain words (docs/ARCHITECTURE.md section 6): what it allows, where it
  asks first and what it never allows, computed from its rules, so the text cannot promise
  something the template does not do; and always that everything else is forbidden.
-->
<script lang="ts">
  import type { Decision, DeviceCatalog, MandateDraft } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { plainWords } from '../../mandate/summary.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    draft: MandateDraft;
    catalog: DeviceCatalog;
    locale: string;
  }

  let { draft, catalog, locale }: Props = $props();

  const GROUPS: readonly { kind: Decision; label: () => string }[] = [
    { kind: 'allow', label: () => m.tpl_summary_allow() },
    { kind: 'ask', label: () => m.tpl_summary_ask() },
    { kind: 'deny', label: () => m.tpl_summary_deny() },
  ];

  const words = $derived(plainWords(draft, catalog, locale));
  const any = $derived(GROUPS.some((g) => words[g.kind].length > 0));
</script>

<div class="plain">
  {#if any}
    <dl>
      {#each GROUPS as group (group.kind)}
        {#if words[group.kind].length > 0}
          <div class="group {group.kind}">
            <dt><Icon name={group.kind} size={16} />{group.label()}</dt>
            {#each words[group.kind] as line (line)}<dd><bdi>{line}</bdi></dd>{/each}
          </div>
        {/if}
      {/each}
    </dl>
  {/if}
  <p class="rest"><Icon name="default" size={16} />{m.tpl_summary_rest()}</p>
</div>

<style>
  .plain {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
  }
  dl {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    margin: 0;
  }
  .group {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  dt {
    display: flex;
    align-items: center;
    gap: 6px;
    font-weight: var(--hm-font-weight-semibold);
  }
  dd {
    margin: 0;
    padding-inline-start: 22px;
    color: var(--hm-color-text);
    overflow-wrap: anywhere;
  }
  .allow dt {
    color: var(--hm-color-allow-fg);
  }
  .ask dt {
    color: var(--hm-color-ask-fg);
  }
  .deny dt {
    color: var(--hm-color-deny-fg);
  }
  .rest {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    color: var(--hm-color-default-fg);
  }
</style>
