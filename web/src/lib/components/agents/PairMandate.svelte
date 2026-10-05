<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Pairing step 3 (design README 6.2, decision G1): the agent gets a new mandate from a
  template, never none and never another agent's. Each option is a card: a base
  template's title and description, and in plain words what the template allows, asks and
  never allows (the rest is forbidden). Hidden base templates are not offered. The display
  name is chosen here; the agent's own name is only the suggestion.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import type { DeviceCatalog, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { NAME_MAX } from '../../mandate/problems.ts';
  import { templateDescription, templateTitle } from '../../mandate/template.ts';
  import { MARK, around } from '../../ui/sentence.ts';
  import { cleanUntrusted, hasVisibleText } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import TextField from '../TextField.svelte';
  import PlainWords from '../mandate/PlainWords.svelte';
  import TemplateBadges from '../mandate/TemplateBadges.svelte';

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
    /** Why the chosen template could not be used (changed or gone meanwhile); shown at the choice. */
    templateError: string;
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
    templateError,
    onback,
    onconfirm,
  }: Props = $props();

  /** Longest claimed name in the heading. */
  const HEADING_MAX = 80;

  const id = $props.id();
  let checked = $state(false);
  /** Approving was tried without a template that is offered. */
  let unchosen = $state(false);
  let nameField: HTMLInputElement | undefined = $state();
  let options: HTMLElement | undefined = $state();

  const title = $derived(around(m.pair_mandate_title({ agent: MARK })));
  const length = $derived([...name.trim()].length);
  // A name must show something: not only blanks, marks or punctuation.
  const nameValid = $derived(length >= 1 && length <= NAME_MAX && hasVisibleText(name));
  const nameError = $derived(checked && !nameValid ? m.validation_name({ max: NAME_MAX }) : '');
  const noTemplates = $derived(templates.length === 0);
  const valid = $derived(templates.some((t) => t.name === chosen));
  // Once the person picks a template, the problem of the earlier one is gone.
  const choiceError = $derived(valid ? '' : templateError || (unchosen ? m.pair_choose_template() : ''));

  async function confirm(event: SubmitEvent) {
    event.preventDefault();
    checked = true;
    if (!nameValid) {
      await tick();
      nameField?.focus();
      return;
    }
    if (busy || noTemplates) return;
    if (!valid) {
      // A template that disappeared is no choice: say so at the choice, not silently.
      unchosen = true;
      await tick();
      options?.querySelector<HTMLInputElement>('input[type="radio"]')?.focus();
      return;
    }
    unchosen = false;
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
  <fieldset aria-describedby={choiceError ? `${id}-choice` : undefined}>
    <legend>{m.pair_mandate_template()}</legend>
    <p id="{id}-choice" class="error choice" role="alert">{#if choiceError}<Icon name="warning" size={16} />{choiceError}{/if}</p>
    <div class="options" bind:this={options}>
      {#each templates as t, i (t.name)}
        <div class="option" class:on={chosen === t.name}>
          <input id="{id}-t{i}" type="radio" name="{id}-template" value={t.name} bind:group={chosen} aria-describedby="{id}-c{i}" />
          <div class="text">
            <span class="title">
              <label class="name" for="{id}-t{i}"><bdi>{templateTitle(t)}</bdi></label>
              <TemplateBadges template={t} />
            </span>
            <div id="{id}-c{i}">
              {#if templateDescription(t)}<p class="description">{templateDescription(t)}</p>{/if}
              <PlainWords draft={t.draft} {catalog} {locale} />
            </div>
          </div>
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
    margin-block: 3px 0;
    margin-inline: 0;
    flex-shrink: 0;
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
  .title {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-1) var(--hm-space-2);
  }
  .description {
    margin-block: 0 var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .error {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .error.choice:has(:global(svg)) {
    margin-block-end: var(--hm-space-2);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: var(--hm-space-3);
  }
</style>
