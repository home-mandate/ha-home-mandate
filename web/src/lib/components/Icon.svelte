<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Icon draws an MDI path (design README section 10) or the own "default" icon, a dashed
  circle with a minus: MDI has no dashed circle, and "no rule allows it" must look different
  from "a rule forbids it". Decorative unless a label is given. Direction icons are mirrored
  in right-to-left layouts through data-rtl-mirror (tokens.css).
-->
<script lang="ts" module>
  import { ICON_PATHS, MIRRORED } from '../icons/paths.ts';

  export type IconName = keyof typeof ICON_PATHS | 'default';
</script>

<script lang="ts">
  interface Props {
    name: IconName;
    size?: 16 | 20 | 32;
    /** Accessible name; without it the icon is hidden from assistive technology. */
    label?: string;
  }

  let { name, size = 20, label }: Props = $props();

  const a11y = $derived(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': 'true' as const });
  const mirror = $derived(MIRRORED.has(name) ? { 'data-rtl-mirror': '' } : {});
</script>

{#if name === 'default'}
  <svg
    viewBox="0 0 24 24"
    width={size}
    height={size}
    focusable="false"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    {...a11y}
  >
    <circle cx="12" cy="12" r="9" stroke-dasharray="2.4 2.6" />
    <path d="M8 12h8" />
  </svg>
{:else}
  <svg viewBox="0 0 24 24" width={size} height={size} focusable="false" {...a11y} {...mirror}>
    <path fill="currentColor" d={ICON_PATHS[name]} />
  </svg>
{/if}

<style>
  svg {
    flex: none;
    vertical-align: middle;
  }
</style>
