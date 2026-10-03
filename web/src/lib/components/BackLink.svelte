<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Link back to the page above, with the direction icon (mirrored in right-to-left layouts).
  Its name says "Back: …", so screen readers do not take it for the navigation link of that
  page; the visible label stays part of the name (WCAG 2.5.3).
-->
<script lang="ts">
  import { m } from '../i18n.ts';
  import { isolate } from '../untrusted.ts';
  import Icon from './Icon.svelte';

  interface Props {
    href: string;
    label: string;
    /** The label is foreign text (a mandate's name): isolated in the name and on screen. */
    untrusted?: boolean;
  }

  let { href, label, untrusted = false }: Props = $props();
</script>

<a class="back" {href} aria-label={m.common_back_to({ page: untrusted ? isolate(label) : label })}
  ><Icon name="back" size={16} />{#if untrusted}<bdi>{label}</bdi>{:else}{label}{/if}</a
>

<style>
  .back {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    min-block-size: var(--hm-size-touch);
    color: var(--hm-color-accent-text);
    font-weight: var(--hm-font-weight-medium);
  }
</style>
