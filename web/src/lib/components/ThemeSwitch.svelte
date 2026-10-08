<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Colour scheme switch in the header (issue #12): one button that cycles System → Light →
  Dark. Its accessible name says the current scheme ("Colour scheme: Dark") and contains the
  visible text (WCAG 2.5.3); a polite live region repeats it after a change, because screen
  readers do not reliably read the new name of the focused button. Below 768 px only the icon
  shows, so it fits next to the emergency stop.
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { appliedTheme, applyTheme, browserStorage, nextTheme, storeTheme, type Theme } from '../app/theme.ts';
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    /** The element the theme is set on (main.ts applied the stored choice to it at start). */
    root?: HTMLElement;
    storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
  }

  let { root = document.documentElement, storage = browserStorage() }: Props = $props();

  const NAMES: Record<Theme, () => string> = {
    system: () => m.theme_system(),
    light: () => m.theme_light(),
    dark: () => m.theme_dark(),
  };
  const ICONS: Record<Theme, IconName> = { system: 'themeSystem', light: 'themeLight', dark: 'themeDark' };

  // svelte-ignore state_referenced_locally (root is read once at start; the switch owns it afterwards)
  let theme: Theme = $state(appliedTheme(root));
  let said = $state('');

  const name = $derived(NAMES[theme]());
  const label = $derived(m.theme_switch_label({ scheme: name }));

  function cycle() {
    theme = nextTheme(theme);
    applyTheme(theme, root);
    storeTheme(theme, storage);
    said = m.theme_switch_label({ scheme: NAMES[theme]() });
  }
</script>

<button type="button" class="theme" aria-label={label} onclick={cycle}>
  <Icon name={ICONS[theme]} />
  <span class="name">{name}</span>
</button>
<span class="hm-visually-hidden" aria-live="polite">{said}</span>

<style>
  .theme {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: var(--hm-size-control);
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: 15px;
    font-weight: var(--hm-font-weight-medium);
    white-space: nowrap;
    color: var(--hm-color-text-muted);
    background: transparent;
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    cursor: pointer;
  }
  .theme:hover {
    color: var(--hm-color-text);
    background: var(--hm-color-surface-hover);
  }
  @media (max-width: 767px) {
    .theme {
      justify-content: center;
      min-inline-size: var(--hm-size-touch);
      min-block-size: var(--hm-size-touch);
      padding-inline: 0;
    }
    .name {
      display: none;
    }
  }
</style>
