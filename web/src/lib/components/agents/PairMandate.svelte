<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing step 3 (design README 6.2, decision G1): the agent gets a new mandate from a
  template, never none and never another agent's ("nothing yet" is the empty template).
  Each option shows in the decision language what the template does. The display name is
  chosen here; the agent's own name is only the suggestion.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import type { DeviceCatalog, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { NAME_MAX } from '../../mandate/problems.ts';
  import { templateChips, templateName } from '../../mandate/template.ts';
  import { MARK, around } from '../../ui/sentence.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import TextField from '../TextField.svelte';
  import DecisionChip from '../mandate/DecisionChip.svelte';

  interface Props {
    templates: readonly Template[];
    catalog: DeviceCatalog;
    locale: string;
    /** Name the agent claims; the suggestion for the display name. */
    claimedName: string;
    headingId: string;
    busy: boolean;
    /** Error of the last attempt, already worded. */
    error: string;
    onback: () => void;
    onconfirm: (template: string, displayName: string) => void;
  }

  let { templates, catalog, locale, claimedName, headingId, busy, error, onback, onconfirm }: Props = $props();

  const id = $props.id();
  // The empty template is the safe start when nothing is chosen ("nothing yet").
  let chosen = $state(untrack(() => (templates.some((t) => t.name === 'empty') ? 'empty' : (templates[0]?.name ?? ''))));
  let name = $state(untrack(() => [...cleanUntrusted(claimedName)].slice(0, NAME_MAX).join('')));
  let checked = $state(false);
  let nameField: HTMLInputElement | undefined = $state();

  const title = $derived(around(m.pair_mandate_title({ agent: MARK })));
  const length = $derived([...name.trim()].length);
  const nameError = $derived(checked && (length < 1 || length > NAME_MAX) ? m.validation_name({ max: NAME_MAX }) : '');

  async function confirm(event: SubmitEvent) {
    event.preventDefault();
    checked = true;
    if (length < 1 || length > NAME_MAX) {
      await tick();
      nameField?.focus();
      return;
    }
    if (busy || chosen === '') return;
    onconfirm(chosen, name.trim());
  }
</script>

<form onsubmit={confirm} novalidate aria-labelledby={headingId}>
  <h2 id={headingId} tabindex="-1">{title[0]}<bdi>{cleanUntrusted(claimedName)}</bdi>{title[1]}</h2>
  <TextField
    label={m.agents_col_name()}
    bind:value={name}
    bind:element={nameField}
    help={m.pair_name_help()}
    error={nameError}
    maxlength={NAME_MAX}
    autocomplete="off"
    dir="auto"
  />
  <fieldset>
    <legend>{m.pair_mandate_template()}</legend>
    <div class="options">
      {#each templates as t (t.name)}
        <label class="option" class:on={chosen === t.name}>
          <input type="radio" name="{id}-template" value={t.name} bind:group={chosen} />
          <span class="text">
            <span class="name"><bdi>{templateName(t.name)}</bdi></span>
            <span class="chips">
              {#each templateChips(t.draft, catalog, locale) as chip (chip.kind)}
                <DecisionChip kind={chip.kind}><bdi>{chip.text}</bdi></DecisionChip>
              {/each}
            </span>
          </span>
        </label>
      {/each}
    </div>
  </fieldset>
  <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
  <div class="actions">
    <Button size="lg" icon="back" disabled={busy} onclick={onback}>{m.common_back()}</Button>
    <Button type="submit" variant="primary" size="lg" {busy}>{m.pair_confirm()}</Button>
  </div>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  h2:focus {
    outline: none;
  }
  fieldset {
    margin: 0;
    padding: 0;
    border: 0;
    min-inline-size: 0;
  }
  legend {
    margin-block-end: var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .options {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .option {
    display: flex;
    gap: var(--hm-space-3);
    align-items: flex-start;
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    background: var(--hm-color-surface);
    cursor: pointer;
  }
  .option.on {
    border-color: var(--hm-color-accent);
    background: var(--hm-color-accent-subtle);
    box-shadow: inset 0 0 0 1px var(--hm-color-accent);
  }
  .option:has(input:focus-visible) {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  input[type='radio'] {
    margin: 3px 0 0;
    inline-size: 18px;
    block-size: 18px;
    accent-color: var(--hm-color-accent);
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    min-inline-size: 0;
  }
  .name {
    font-weight: var(--hm-font-weight-semibold);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .error {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--hm-space-3);
  }
</style>
