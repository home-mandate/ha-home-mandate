<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  A template on the mandate list: its name, how many rules it has and, in the decision
  language, what those rules do. The chips are computed from the template's rules, so the
  card cannot promise something the template does not do.
-->
<script lang="ts">
  import type { DeviceCatalog, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { templateChips, templateName } from '../../mandate/template.ts';
  import Button from '../Button.svelte';
  import DecisionChip from './DecisionChip.svelte';

  interface Props {
    template: Template;
    catalog: DeviceCatalog;
    locale: string;
    onuse: (name: string) => void;
  }

  let { template, catalog, locale, onuse }: Props = $props();

  const id = $props.id();
  const chips = $derived(templateChips(template.draft, catalog, locale));
</script>

<article aria-labelledby="{id}-name">
  <h3 id="{id}-name"><bdi>{templateName(template.name)}</bdi></h3>
  <p>{m.mandates_rules_count({ count: template.draft.rules.length })}</p>
  <ul role="list">
    {#each chips as chip (chip.kind)}
      <li><DecisionChip kind={chip.kind}><bdi>{chip.text}</bdi></DecisionChip></li>
    {/each}
  </ul>
  <Button size="lg" aria-label="{m.tpl_use()}: {templateName(template.name)}" onclick={() => onuse(template.name)}>{m.tpl_use()}</Button>
</article>

<style>
  article {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    padding: 18px var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    box-shadow: var(--hm-shadow-sm);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  p {
    margin: 0;
    font-size: 15px;
    color: var(--hm-color-text-muted);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    flex: 1;
    align-content: flex-start;
  }
</style>
