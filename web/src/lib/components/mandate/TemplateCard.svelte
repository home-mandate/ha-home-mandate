<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  A template to start from on the mandate list: its title (a base template's in the UI
  language, otherwise its name), what it is for, and in plain words what it allows, asks
  and never allows. The words are computed from the template's rules, so the card cannot
  promise something the template does not do.
-->
<script lang="ts">
  import type { DeviceCatalog, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { templateDescription, templateTitle } from '../../mandate/template.ts';
  import Button from '../Button.svelte';
  import PlainWords from './PlainWords.svelte';
  import TemplateBadges from './TemplateBadges.svelte';

  interface Props {
    template: Template;
    catalog: DeviceCatalog;
    locale: string;
    onuse: (name: string) => void;
  }

  let { template, catalog, locale, onuse }: Props = $props();

  const id = $props.id();
  const title = $derived(templateTitle(template));
  const description = $derived(templateDescription(template));
</script>

<article aria-labelledby="{id}-name">
  <div class="head">
    <h3 id="{id}-name"><bdi>{title}</bdi></h3>
    <TemplateBadges {template} />
  </div>
  {#if description}<p class="description">{description}</p>{/if}
  <p class="count">{m.mandates_rules_count({ count: template.draft.rules.length })}</p>
  <div class="words"><PlainWords draft={template.draft} {catalog} {locale} /></div>
  <Button size="lg" aria-label="{m.tpl_use()}: {title}" onclick={() => onuse(template.name)}>{m.tpl_use()}</Button>
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
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-1) var(--hm-space-2);
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
    text-wrap: pretty;
  }
  .count {
    font-size: var(--hm-font-size-sm);
  }
  .words {
    flex: 1;
    align-self: stretch;
  }
</style>
