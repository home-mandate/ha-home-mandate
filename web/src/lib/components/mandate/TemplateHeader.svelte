<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Head of the template editor: way back, title (a base template's in the UI language, the
  household's own by its name), its marks and description, the save state, the actions
  and the problems that block saving (each one leads to its field). Base templates are
  never changed: they can be loaded, saved as a new template and hidden or shown again.
  Split from TemplateEditor; the page owns all state.
-->
<script lang="ts">
  import type { TemplateSummary } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import type { FieldProblem } from '../../mandate/problems.ts';
  import BackLink from '../BackLink.svelte';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import TemplateBadges from './TemplateBadges.svelte';

  interface Props {
    backHref: string;
    title: string;
    description: string;
    /** The stored template; null for a new one. */
    template: Pick<TemplateSummary, 'builtin' | 'hidden'> | null;
    /** Unsaved changes. */
    changes: number;
    /** The own template can be saved in place (it exists and is not outdated or removed). */
    saveable: boolean;
    /** Problems shown to the person; each leads to its field. */
    problems: readonly FieldProblem[];
    busy: boolean;
    onload: () => void;
    onsave: () => void;
    onsaveas: () => void;
    ondelete: () => void;
    onhidden: (hidden: boolean) => void;
    onshow: (problem: FieldProblem) => void;
    /** The problem list, for the editor to move the focus to. */
    summary?: HTMLElement;
    /** The page heading, for the editor to move the focus to (after discarding). */
    heading?: HTMLElement;
  }

  let {
    backHref,
    title,
    description,
    template,
    changes,
    saveable,
    problems,
    busy,
    onload,
    onsave,
    onsaveas,
    ondelete,
    onhidden,
    onshow,
    summary = $bindable(),
    heading = $bindable(),
  }: Props = $props();

  const uid = $props.id();
  const own = $derived(template !== null && !template.builtin);
  const problemText = (p: FieldProblem) => (p.rule === null ? p.text : m.validation_in_rule({ rule: m.rule_ref({ n: p.rule + 1 }), problem: p.text }));
  const blocked = $derived(problems.length > 0 ? `${uid}-problems` : undefined);
</script>

<div class="head">
  <BackLink href={backHref} label={m.templates_title()} />
  <div class="title">
    <h1 bind:this={heading} tabindex="-1"><bdi>{title}</bdi></h1>
    {#if template}<TemplateBadges {template} />{/if}
  </div>
  {#if description}<p class="description">{description}</p>{/if}
  <div class="bar">
    <span id="{uid}-state" class="state" class:dirty={changes > 0}>
      <span class="mark" aria-hidden="true"></span>{changes > 0 ? m.editor_unsaved({ count: changes }) : m.editor_saved_state()}
    </span>
    <!-- Announced when the state flips, not with every change of the count. -->
    <span class="hm-visually-hidden" role="status">{changes > 0 ? m.editor_unsaved_any() : m.editor_saved_state()}</span>
    <div class="actions">
      <Button size="lg" icon="list" onclick={onload}>{m.template_load()}</Button>
      {#if template?.builtin}
        <Button size="lg" disabled={busy} onclick={() => onhidden(!template?.hidden)}>{template.hidden ? m.template_show() : m.template_hide()}</Button>
      {/if}
      {#if own}
        <Button size="lg" variant="danger" disabled={busy} onclick={ondelete}>{m.common_delete()}</Button>
      {/if}
      <Button size="lg" variant={own ? 'secondary' : 'primary'} aria-describedby={blocked} onclick={onsaveas}>{m.template_save_as()}</Button>
      {#if own}
        <Button
          variant="primary"
          size="lg"
          disabled={!saveable || (changes === 0 && problems.length === 0)}
          aria-describedby={blocked ?? (changes === 0 ? `${uid}-state` : undefined)}
          aria-keyshortcuts="Control+S Meta+S"
          onclick={onsave}
        >
          {m.editor_save()}
        </Button>
      {/if}
    </div>
  </div>
  {#if problems.length > 0}
    <div bind:this={summary} id="{uid}-problems" class="problems" role="group" aria-labelledby="{uid}-problem-count" tabindex="-1">
      <span id="{uid}-problem-count" class="count" role="alert"><Icon name="warning" />{m.validation_summary({ count: problems.length })}</span>
      <ul role="list">
        {#each problems as p (`${p.rule}/${p.part}/${p.text}`)}
          <li><button type="button" onclick={() => onshow(p)}>{problemText(p)}</button></li>
        {/each}
      </ul>
    </div>
  {/if}
</div>

<style>
  .head {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .title,
  .bar,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
  }
  .bar {
    justify-content: space-between;
    gap: var(--hm-space-3) var(--hm-space-4);
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .description {
    margin: 0;
    max-inline-size: 72ch;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  .state {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-subtle);
  }
  .state.dirty {
    color: var(--hm-color-text);
  }
  .mark {
    inline-size: 8px;
    block-size: 8px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-positive-fg);
  }
  .dirty .mark {
    background: var(--hm-color-accent);
  }
  .problems {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: 10px;
    font-size: 15px;
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-danger-bg);
    border: var(--hm-border-width) solid var(--hm-color-danger-border);
  }
  .count {
    display: flex;
    align-items: center;
    gap: 10px;
    font-weight: var(--hm-font-weight-medium);
  }
  .problems ul {
    margin: 0;
    padding-inline-start: 30px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .problems button {
    min-block-size: 32px;
    padding: 0;
    border: none;
    background: transparent;
    font: inherit;
    font-size: var(--hm-font-size-sm);
    text-align: start;
    text-decoration: underline;
    color: inherit;
    cursor: pointer;
  }
  @media (pointer: coarse), (max-width: 767px) {
    .problems button {
      min-block-size: var(--hm-size-touch);
    }
  }
  @media (max-width: 767px) {
    .actions,
    .state {
      flex: 1 1 100%;
    }
    .actions :global(.btn) {
      flex: 1 1 auto;
      justify-content: center;
    }
  }
</style>
