<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One rule in the list (design README 6.5): move buttons, its number, the sentence, computed
  notes and how many devices it matches. "Edit" opens the form beneath; the sentence stays
  visible and updates live. A rule with errors keeps a red outline and its messages while
  collapsed. Move buttons stay focusable at the ends of the list, so the focus is not lost.
-->
<script lang="ts">
  import { tick, type Snippet } from 'svelte';
  import type { Decision } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import type { RuleNote } from '../../mandate/notes.ts';
  import type { RuleText } from '../../mandate/text.ts';
  import Button from '../Button.svelte';
  import Icon, { type IconName } from '../Icon.svelte';
  import RuleSentence from './RuleSentence.svelte';

  interface Props {
    /** Position in the list, starting at 1. */
    n: number;
    count: number;
    text: RuleText;
    decision: Decision;
    notes: readonly RuleNote[];
    /** Messages of problems that may be shown now. */
    errors: readonly string[];
    matches: string;
    editing: boolean;
    /** The form can edit this rule. */
    editable: boolean;
    /** No controls at all: the mandate cannot be changed. */
    readonly: boolean;
    onedit: () => void;
    onmove: (to: number) => void;
    onremove: () => void;
    ondragstart: () => void;
    ondrop: () => void;
    /** Button that opened the form; gets the focus back when it closes. */
    opener?: HTMLButtonElement;
    children?: Snippet;
    /** Shown under the closed rule, e.g. that its change is not saved yet. */
    after?: Snippet;
  }

  let {
    n,
    count,
    text,
    decision,
    notes,
    errors,
    matches,
    editing,
    editable,
    readonly,
    onedit,
    onmove,
    onremove,
    ondragstart,
    ondrop,
    opener = $bindable(),
    children,
    after,
  }: Props = $props();

  const ICONS: Record<RuleNote['kind'], IconName> = { critical: 'critical', danger: 'warning', info: 'info' };

  let over = $state(false);
  let up: HTMLButtonElement | undefined = $state();
  let down: HTMLButtonElement | undefined = $state();

  /** Moving the rule moves its element, which drops the focus: put it back on the button. */
  async function move(to: number, button: HTMLButtonElement | undefined) {
    // At the ends the parent says that there is nowhere to go.
    onmove(to);
    await tick();
    button?.focus();
  }

  function dragstart(event: DragEvent) {
    if (event.dataTransfer) {
      event.dataTransfer.effectAllowed = 'move';
      // Some browsers start a drag only with data set.
      event.dataTransfer.setData('text/plain', String(n));
    }
    ondragstart();
  }

  function dragover(event: DragEvent) {
    event.preventDefault();
    over = true;
  }

  function drop(event: DragEvent) {
    event.preventDefault();
    over = false;
    ondrop();
  }
</script>

<li class:editing class:invalid={errors.length > 0} class:over ondragover={readonly ? undefined : dragover} ondragleave={() => (over = false)} ondrop={readonly ? undefined : drop}>
  <div class="head">
    {#if !readonly}
      <div class="move">
        <button bind:this={up} type="button" class="up" aria-label={m.rule_move_up({ n })} aria-disabled={n === 1 ? 'true' : undefined} onclick={() => move(n - 1, up)}>
          <Icon name="chevronDown" size={16} />
        </button>
        <span class="handle" draggable="true" title={m.rule_drag({ n })} aria-hidden="true" ondragstart={dragstart}><Icon name="drag" size={16} /></span>
        <button bind:this={down} type="button" aria-label={m.rule_move_down({ n })} aria-disabled={n === count ? 'true' : undefined} onclick={() => move(n + 1, down)}>
          <Icon name="chevronDown" size={16} />
        </button>
      </div>
    {/if}
    <span class="number" aria-hidden="true">{n}</span>
    <div class="body">
      <p><span class="hm-visually-hidden">{`${m.rule_ref({ n })}: `}</span><RuleSentence {text} {decision} /></p>
      {#if text.conditions}<span class="line"><Icon name="history" size={16} /><span>{text.conditions}</span></span>{/if}
      {#each notes as note (note.text)}
        <span class="line {note.kind}"><Icon name={ICONS[note.kind]} size={16} /><span>{note.text}</span></span>
      {/each}
      {#if !editing}
        {#each errors as error (error)}
          <span class="line danger"><Icon name="warning" size={16} /><span>{error}</span></span>
        {/each}
      {/if}
      <span class="matches">{matches}</span>
    </div>
    {#if !readonly && !editing}
      {#if editable}
        <Button bind:element={opener} variant="text" size="lg" aria-label="{m.common_edit()}: {m.rule_ref({ n })}" onclick={onedit}>{m.common_edit()}</Button>
      {:else}
        <Button bind:element={opener} variant="text" size="lg" class="remove" aria-label="{m.common_delete()}: {m.rule_ref({ n })}" onclick={onremove}>
          {m.common_delete()}
        </Button>
      {/if}
    {/if}
  </div>
  {#if editing && children}{@render children()}{:else if !editing && after}{@render after()}{/if}
</li>

<style>
  li {
    display: flex;
    flex-direction: column;
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    /* Keeps the box size when the border gets thicker. */
    outline: 1px solid transparent;
    outline-offset: -2px;
  }
  li.editing {
    border-color: var(--hm-color-accent);
    outline-color: var(--hm-color-accent);
    box-shadow: var(--hm-shadow-md);
  }
  li.invalid {
    border-color: var(--hm-color-danger-fg);
    outline-color: var(--hm-color-danger-fg);
  }
  li.over {
    background: var(--hm-color-surface-hover);
  }
  .head {
    display: flex;
    align-items: flex-start;
    gap: 2px;
    padding-block: var(--hm-space-2);
    padding-inline: var(--hm-space-1) var(--hm-space-2);
  }
  .move {
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
  }
  .move button {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 32px;
    block-size: 28px;
    padding: 0;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--hm-color-text-muted);
    cursor: pointer;
  }
  .move button:hover {
    background: var(--hm-color-surface-hover);
  }
  .move button[aria-disabled='true'] {
    color: var(--hm-color-text-disabled);
    background: transparent;
    cursor: not-allowed;
  }
  .up {
    rotate: 180deg;
  }
  .handle {
    display: flex;
    align-items: center;
    justify-content: center;
    inline-size: 32px;
    block-size: 20px;
    color: var(--hm-color-text-subtle);
    cursor: grab;
  }
  .number {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    min-inline-size: 28px;
    block-size: 28px;
    margin-block-start: 10px;
    border-radius: 6px;
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
    font-variant-numeric: tabular-nums;
    background: var(--hm-color-surface-sunken);
    color: var(--hm-color-text-muted);
  }
  .editing .number {
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
  }
  .body {
    flex: 1;
    min-inline-size: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: var(--hm-space-3) var(--hm-space-2) var(--hm-space-1);
  }
  p {
    margin: 0;
  }
  .line {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
    overflow-wrap: anywhere;
  }
  .line :global(svg) {
    flex-shrink: 0;
    margin-block-start: 2px;
  }
  .line.critical {
    color: var(--hm-color-critical-fg);
  }
  .line.danger {
    color: var(--hm-color-danger-fg);
  }
  .line.info {
    color: var(--hm-color-info-fg);
  }
  .matches {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .head :global(.btn) {
    flex-shrink: 0;
  }
  .head :global(.remove) {
    color: var(--hm-color-danger-fg);
  }
  @media (pointer: coarse), (max-width: 767px) {
    .move button {
      inline-size: var(--hm-size-touch);
      block-size: var(--hm-size-touch);
    }
    .handle {
      display: none;
    }
    /* The sentence gets the width; the button moves to its own line. */
    .head {
      flex-wrap: wrap;
    }
    .body {
      flex-basis: 0;
    }
    .head :global(.btn) {
      flex-basis: 100%;
      justify-content: flex-end;
    }
  }
  @media (forced-colors: active) {
    li.editing,
    li.invalid {
      outline-color: Highlight;
    }
  }
</style>
