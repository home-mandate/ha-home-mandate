<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The mark of a chip or an approver: a check when selected, a muted x when not. Both icons have
  the same size, so toggling never changes the control's width (issue #47). Hidden from
  assistive technology: aria-pressed carries the state, a screen reader must not read "x".
  The muted x is text-muted, which holds 4.5:1 on the surface in both schemes (tokens.test.ts);
  in forced colours both marks take the text colour.
-->
<script lang="ts">
  import Icon from './Icon.svelte';

  interface Props {
    on: boolean;
  }

  let { on }: Props = $props();
</script>

<span class="mark" class:off={!on} data-mark={on ? 'on' : 'off'} aria-hidden="true">
  <Icon name={on ? 'check' : 'close'} size={16} />
</span>

<style>
  .mark {
    display: flex;
    flex: none;
  }
  .off {
    color: var(--hm-color-text-muted);
  }
</style>
