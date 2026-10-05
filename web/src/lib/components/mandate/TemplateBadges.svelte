<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Marks of a template next to its title: a base template that ships with Home-Mandate, and
  one hidden from admission. Words with a border, never colour alone.
-->
<script lang="ts">
  import type { TemplateSummary } from '../../api/types.ts';
  import { m } from '../../i18n.ts';

  interface Props {
    template: Pick<TemplateSummary, 'builtin' | 'hidden'>;
  }

  let { template }: Props = $props();
</script>

{#if template.builtin}<span class="badge">{m.tpl_builtin()}</span>{/if}
{#if template.hidden}<span class="badge hidden">{m.tpl_hidden()}</span>{/if}

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    padding-block: 1px;
    padding-inline: var(--hm-space-2);
    border-radius: var(--hm-radius-pill);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-medium);
    line-height: var(--hm-line-height-normal);
    color: var(--hm-color-text-muted);
    background: var(--hm-color-surface-sunken);
    white-space: nowrap;
  }
  .hidden {
    border-style: dashed;
  }
</style>
