<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  A newer version arrived while the editor has unsaved changes (decision E6): put the
  changes on top of it, or discard them. Split from MandateEditor.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    /** Number of the newer version. */
    version: number;
    onkeep: () => void;
    ondiscard: () => void;
  }

  let { version, onkeep, ondiscard }: Props = $props();
</script>

<div class="conflict" role="alert">
  <span class="lead"><Icon name="warning" /><strong>{m.editor_conflict_title()}</strong></span>
  <span>{m.editor_conflict_body({ version })}</span>
  <div class="choices">
    <Button size="lg" onclick={onkeep}>{m.editor_conflict_keep({ version })}</Button>
    <Button size="lg" onclick={ondiscard}>{m.editor_conflict_discard()}</Button>
  </div>
</div>

<style>
  .conflict {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: 10px;
    font-size: 15px;
    color: var(--hm-color-text);
    background: var(--hm-color-warning-bg);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
  }
  .lead {
    display: flex;
    align-items: center;
    gap: 10px;
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-warning-fg);
  }
  .choices {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
</style>
