<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Applying a template to a mandate that still carries the name of the template its rules
  came from (#16): propose the agent's name; the human takes it or keeps the current one.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import { isolate } from '../../untrusted.ts';

  interface Props {
    /** The mandate's current name and the proposed one; untrusted text, shown cleaned. */
    current: string;
    proposed: string;
    /** True: rename to the proposed name. */
    rename: boolean;
  }

  let { current, proposed, rename = $bindable() }: Props = $props();

  const id = $props.id();
  const TAKE = 'take';
  const KEEP = 'keep';
  const choice = $derived(rename ? TAKE : KEEP);
</script>

<fieldset aria-describedby="{id}-why">
  <legend>{m.agent_detail_rename_legend()}</legend>
  <p id="{id}-why" class="why">{m.agent_detail_rename_why()}</p>
  <label>
    <input type="radio" name="{id}-name" value={TAKE} checked={choice === TAKE} onchange={() => (rename = true)} />
    <span>{m.agent_detail_rename_take({ name: isolate(proposed) })}</span>
  </label>
  <label>
    <input type="radio" name="{id}-name" value={KEEP} checked={choice === KEEP} onchange={() => (rename = false)} />
    <span>{m.agent_detail_rename_keep({ name: isolate(current) })}</span>
  </label>
</fieldset>

<style>
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    align-self: stretch;
    margin: 0;
    padding: 0;
    border: 0;
    min-inline-size: 0;
  }
  legend {
    margin-block-end: var(--hm-space-1);
    padding: 0;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .why {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  label {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-touch);
    overflow-wrap: anywhere;
  }
  input[type='radio'] {
    flex-shrink: 0;
    margin-block: 3px 0;
    margin-inline: 0;
    inline-size: 18px;
    block-size: 18px;
    accent-color: var(--hm-color-accent);
  }
  input[type='radio']:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
</style>
