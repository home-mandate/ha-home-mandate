<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  "Load template": any template into the editor, base templates included, hidden ones too
  (marked). Each is a link to it in the editor; from there it can be saved as a new
  template. Unsaved changes of the current edit are lost, and the dialog says so first.
-->
<script lang="ts">
  import type { TemplateSummary } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { templateTitle } from '../../mandate/template.ts';
  import { href } from '../../router.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';
  import TemplateBadges from './TemplateBadges.svelte';

  interface Props {
    open: boolean;
    templates: readonly TemplateSummary[];
    /** Name of the template in the editor; null for a new one. */
    current: string | null;
    /** Unsaved changes that loading would discard. */
    changes: number;
    onclose: () => void;
  }

  let { open, templates, current, changes, onclose }: Props = $props();

  const id = $props.id();
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" {onclose}>
  <div class="content">
    <h2 id="{id}-title">{m.template_load_title()}</h2>
    <p id="{id}-body" class:warn={changes > 0}>
      {#if changes > 0}<Icon name="warning" size={16} />{/if}{changes > 0 ? m.template_load_unsaved() : m.template_load_body()}
    </p>
    <ul role="list">
      {#each templates as t (t.name)}
        <li>
          <!-- The template already open: choosing it just closes the dialog. -->
          <a
            href={href({ name: 'template', template: t.name })}
            aria-current={t.name === current ? 'page' : undefined}
            onclick={(event) => {
              if (t.name !== current) return;
              event.preventDefault();
              onclose();
            }}
          >
            <bdi>{templateTitle(t)}</bdi>
            <span class="badges"><TemplateBadges template={t} /></span>
          </a>
        </li>
      {/each}
    </ul>
    <div class="actions">
      <Button size="lg" onclick={onclose}>{m.common_cancel()}</Button>
    </div>
  </div>
</Dialog>

<style>
  .content {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: 15px;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  p.warn {
    color: var(--hm-color-warning-fg);
  }
  p :global(svg) {
    flex-shrink: 0;
    margin-block-start: 3px;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    max-block-size: 50dvh;
    overflow-y: auto;
  }
  a {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-1) var(--hm-space-2);
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding: var(--hm-space-2) var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text);
    text-decoration: none;
  }
  a:hover {
    background: var(--hm-color-surface-hover);
  }
  a:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: -2px;
  }
  a[aria-current='page'] {
    border-color: var(--hm-color-accent);
    background: var(--hm-color-accent-subtle);
  }
  .badges {
    display: inline-flex;
    gap: var(--hm-space-1);
  }
  .actions {
    display: flex;
    justify-content: flex-end;
  }
</style>
