<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The logo, house with key (design update 2026-10). Two images, of which the tokens
  --hm-logo-light / --hm-logo-dark show one, so the logo follows the system scheme and a forced
  data-hm-theme without script and without flicker. Decorative: the product name stands next
  to it everywhere. Not square: the width follows the height at 1632:1766.
-->
<script lang="ts">
  interface Props {
    /** Height in px. */
    size?: number;
  }

  let { size = 36 }: Props = $props();

  const width = $derived(Math.round((size * 1632) / 1766));
</script>

<img class="light" src="./hm-logo.svg" alt="" {width} height={size} />
<img class="dark" src="./hm-logo-dark.svg" alt="" {width} height={size} />

<style>
  img {
    flex-shrink: 0;
  }
  .light {
    display: var(--hm-logo-light, block);
  }
  .dark {
    display: var(--hm-logo-dark, none);
  }
</style>
