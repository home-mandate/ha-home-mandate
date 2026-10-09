<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Head of the mandate editor: way back, name, status and version, the save state, the link
  to the versions, the save button and the list of problems that block saving (each one
  leads to its field). Split from MandateEditor; the editor owns all state.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import type { EffectiveStatus } from '../../mandate/dates.ts';
  import type { FieldProblem } from '../../mandate/problems.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import BackLink from '../BackLink.svelte';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import MandateStatus from './MandateStatus.svelte';
  import VersionChip from './VersionChip.svelte';

  interface Props {
    backHref: string;
    versionsHref: string;
    /** The name being edited; the stored name while it is empty. */
    name: string;
    storedName: string;
    status: EffectiveStatus;
    version: number;
    digest: string;
    readonly: boolean;
    /** Unsaved changes. */
    changes: number;
    /** Nothing to save and nothing to show: the button names the saved state. */
    idle: boolean;
    /** Problems shown to the person; each leads to its field. */
    problems: readonly FieldProblem[];
    onsave: () => void;
    onshow: (problem: FieldProblem) => void;
    /** The problem list, for the editor to move the focus to. */
    summary?: HTMLElement;
    /** The page heading, for the editor to move the focus to (after discarding). */
    heading?: HTMLElement;
  }

  let {
    backHref,
    versionsHref,
    name,
    storedName,
    status,
    version,
    digest,
    readonly,
    changes,
    idle,
    problems,
    onsave,
    onshow,
    summary = $bindable(),
    heading = $bindable(),
  }: Props = $props();

  const uid = $props.id();
  const problemText = (p: FieldProblem) => (p.rule === null ? p.text : m.validation_in_rule({ rule: m.rule_ref({ n: p.rule + 1 }), problem: p.text }));
</script>

<div class="head">
  <BackLink href={backHref} label={m.editor_back()} />
  <div class="bar">
    <div class="title">
      <h1 bind:this={heading} tabindex="-1"><bdi>{cleanUntrusted(name) || cleanUntrusted(storedName)}</bdi></h1>
      <MandateStatus {status} />
      <VersionChip {version} {digest} />
    </div>
    <div class="actions">
      {#if !readonly}
        <span id="{uid}-state" class="state" class:dirty={changes > 0}>
          <span class="mark" aria-hidden="true"></span>{changes > 0 ? m.editor_unsaved({ count: changes }) : m.editor_saved_state()}
        </span>
        <!-- Announced when the state flips, not with every change of the count. -->
        <span class="hm-visually-hidden" role="status">{changes > 0 ? m.editor_unsaved_any() : m.editor_saved_state()}</span>
      {/if}
      <a class="versions" href={versionsHref}><Icon name="history" />{m.editor_versions()}</a>
      {#if !readonly}
        <Button
          variant="primary"
          size="lg"
          disabled={idle}
          aria-describedby={problems.length > 0 ? `${uid}-problems` : changes === 0 ? `${uid}-state` : undefined}
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
  .bar,
  .title,
  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-3);
  }
  .bar {
    gap: var(--hm-space-3) var(--hm-space-4);
  }
  .title {
    flex: 1 1 320px;
    min-inline-size: 0;
  }
  h1 {
    margin: 0;
    font-size: var(--hm-font-size-2xl);
    line-height: var(--hm-line-height-tight);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
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
  .versions {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding-inline: 14px;
    border-radius: var(--hm-radius-md);
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    text-decoration: none;
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  .versions:hover {
    background: var(--hm-color-surface-hover);
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
    .state {
      flex: 1 1 100%;
    }
    .versions,
    .actions :global(.btn) {
      flex: 1 1 0;
      justify-content: center;
    }
    .actions {
      flex: 1 1 100%;
    }
  }
</style>
