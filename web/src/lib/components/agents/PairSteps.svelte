<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Progress of the pairing: three named steps, the current one marked with aria-current="step". -->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import Icon from '../Icon.svelte';

  interface Props {
    /** 1–3. */
    current: number;
  }

  let { current }: Props = $props();

  const labels = $derived([m.pair_step_code(), m.pair_step_verify(), m.pair_step_mandate()]);
</script>

<ol aria-label={m.pair_step_label({ n: current, total: labels.length })}>
  {#each labels as label, i (i)}
    {@const n = i + 1}
    <li class:done={n < current} class:current={n === current} aria-current={n === current ? 'step' : undefined}>
      <span class="bar" aria-hidden="true"></span>
      <span class="label">{#if n < current}<Icon name="check" size={16} />{/if}{label}</span>
    </li>
  {/each}
</ol>

<style>
  ol {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--hm-space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text-subtle);
  }
  .bar {
    block-size: 4px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-border);
  }
  .label {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    overflow-wrap: anywhere;
  }
  .done {
    color: var(--hm-color-positive-fg);
  }
  .done .bar {
    background: var(--hm-color-positive-fg);
  }
  .current {
    color: var(--hm-color-text);
    font-weight: var(--hm-font-weight-semibold);
  }
  .current .bar {
    background: var(--hm-color-accent);
  }
  @media (forced-colors: active) {
    .bar {
      border: 1px solid CanvasText;
    }
    .done .bar,
    .current .bar {
      background: CanvasText;
    }
  }
</style>
