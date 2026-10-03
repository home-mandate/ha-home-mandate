<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing step 3 (design README 6.2, decision G1): the agent gets a new mandate from a
  template, never none and never another agent's ("nothing yet" is the empty template).
  Each option shows in the decision language what the template does. The display name is
  chosen here; the agent's own name is only the suggestion.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import type { DeviceCatalog, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { NAME_MAX } from '../../mandate/problems.ts';
  import { templateChips, templateName } from '../../mandate/template.ts';
  import { MARK, around } from '../../ui/sentence.ts';
  import { cleanUntrusted, hasVisibleText } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import TextField from '../TextField.svelte';
  import DecisionChip from '../mandate/DecisionChip.svelte';

  interface Props {
    templates: readonly Template[];
    catalog: DeviceCatalog;
    locale: string;
    /** Name the agent claims, for the heading. */
    claimedName: string;
    /** Chosen template and display name; kept by the page, so going back loses nothing. */
    chosen: string;
    name: string;
    headingId: string;
    busy: boolean;
    /** Error of the last attempt, already worded. */
    error: string;
    onback: () => void;
    onconfirm: (template: string, displayName: string) => void;
  }

  let {
    templates,
    catalog,
    locale,
    claimedName,
    chosen = $bindable(),
    name = $bindable(),
    headingId,
    busy,
    error,
    onback,
    onconfirm,
  }: Props = $props();

  /** Longest claimed name in the heading. */
  const HEADING_MAX = 80;

  const id = $props.id();
  let checked = $state(false);
  let nameField: HTMLInputElement | undefined = $state();

  const title = $derived(around(m.pair_mandate_title({ agent: MARK })));
  const length = $derived([...name.trim()].length);
  // A name must show something: not only blanks, marks or punctuation.
  const nameValid = $derived(length >= 1 && length <= NAME_MAX && hasVisibleText(name));
  const nameError = $derived(checked && !nameValid ? m.validation_name({ max: NAME_MAX }) : '');
  const noTemplates = $derived(templates.length === 0);

  async function confirm(event: SubmitEvent) {
    event.preventDefault();
    checked = true;
    if (!nameValid) {
      await tick();
      nameField?.focus();
      return;
    }
    if (busy || noTemplates || !templates.some((t) => t.name === chosen)) return;
    onconfirm(chosen, name.trim());
  }
</script>

<form onsubmit={confirm} novalidate aria-labelledby={headingId}>
  <h2 id={headingId} tabindex="-1">{title[0]}<bdi>{cleanUntrusted(claimedName, HEADING_MAX)}</bdi>{title[1]}</h2>
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
      {#each templates as t, i (t.name)}
        <div class="option" class:on={chosen === t.name}>
          <input id="{id}-t{i}" type="radio" name="{id}-template" value={t.name} bind:group={chosen} aria-describedby="{id}-c{i}" />
          <span class="text">
            <label class="name" for="{id}-t{i}"><bdi>{templateName(t.name)}</bdi></label>
            <span class="chips" id="{id}-c{i}">
              {#each templateChips(t.draft, catalog, locale) as chip (chip.kind)}
                <DecisionChip kind={chip.kind}><bdi>{chip.text}</bdi></DecisionChip>
              {/each}
            </span>
          </span>
        </div>
      {/each}
    </div>
  </fieldset>
  <p class="error" role="alert">{#if error || noTemplates}<Icon name="warning" size={16} />{error || m.pair_failed()}{/if}</p>
  <div class="actions">
    <Button size="lg" icon="back" disabled={busy} onclick={onback}>{m.common_back()}</Button>
    <Button type="submit" variant="primary" size="lg" {busy} disabled={noTemplates}>{m.pair_confirm()}</Button>
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
    cursor: pointer;
  }
  @media (forced-colors: active) {
    .option.on {
      outline: 2px solid Highlight;
    }
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
