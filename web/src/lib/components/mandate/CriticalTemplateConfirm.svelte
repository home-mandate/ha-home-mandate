<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The separate confirmation (decision U9) when a template is to become an agent's mandate
  and its rules allow critical actions without approval: on pairing, for a new mandate and
  when a template replaces the rules of a mandate. The server asks for it (answer
  critical_confirmation_required); this box names the template, the agent and every such
  rule. It is inline, not a second dialog: the safe choice comes first and gets the focus,
  only "Allow without approval" sends the confirmation. Escape cancels.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import type { DeviceCatalog, Rule } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { ruleText } from '../../mandate/text.ts';
  import { templateName } from '../../mandate/template.ts';
  import { MARK, MARK2, around } from '../../ui/sentence.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import RuleSentence from './RuleSentence.svelte';

  interface Props {
    /** Name of the template. */
    template: string;
    /** Name of the agent; untrusted. */
    agent: string;
    /** The template's rules with allow_critical; null when they could not be loaded. */
    rules: readonly Rule[] | null;
    catalog: DeviceCatalog;
    locale: string;
    busy: boolean;
    oncancel: () => void;
    onconfirm: () => void;
  }

  let { template, agent, rules, catalog, locale, busy, oncancel, onconfirm }: Props = $props();

  const id = $props.id();
  let cancel: HTMLButtonElement | undefined = $state();

  // Sentence split around the two names, so each sits in its own <bdi>.
  const parts = $derived(around(m.template_critical_body({ template: MARK, agent: MARK2 })));
  const tail = $derived((parts[1] ?? '').split(MARK2));

  onMount(() => cancel?.focus());

  function key(event: KeyboardEvent) {
    if (event.key !== 'Escape' || busy) return;
    event.preventDefault();
    event.stopPropagation();
    oncancel();
  }
</script>

<div class="box" role="alertdialog" tabindex="-1" aria-modal="false" aria-labelledby="{id}-title" aria-describedby="{id}-body" onkeydown={key}>
  <h3 id="{id}-title"><Icon name="critical" size={20} /><span>{m.template_critical_title()}</span></h3>
  <p id="{id}-body">
    {parts[0]}<bdi>{templateName(template)}</bdi>{tail[0]}<bdi>{cleanUntrusted(agent)}</bdi>{tail[1] ?? ''}
  </p>
  {#if rules && rules.length > 0}
    <ul role="list">
      {#each rules as rule (rule.id)}
        <li><RuleSentence text={ruleText(rule, catalog, locale)} decision={rule.decision} size="sm" critical /></li>
      {/each}
    </ul>
  {:else}
    <p class="unknown">{m.template_critical_unknown()}</p>
  {/if}
  <p class="hint">{m.template_critical_hint()}</p>
  <div class="actions">
    <Button bind:element={cancel} size="lg" disabled={busy} onclick={oncancel}>{m.common_cancel()}</Button>
    <Button variant="danger" size="lg" {busy} onclick={onconfirm}>{m.critical_confirm_action()}</Button>
  </div>
</div>

<style>
  .box {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    border: var(--hm-border-width) solid var(--hm-color-danger-border);
    background: var(--hm-color-danger-bg);
  }
  h3 {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    margin: 0;
    font-size: 17px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
  }
  p {
    margin: 0;
    font-size: 15px;
    text-wrap: pretty;
    overflow-wrap: anywhere;
  }
  .hint,
  .unknown {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  ul {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
</style>
