<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Emergency stop (design README 6.11 section 6; decision S7). Triggering opens the same
  sheet as the header (hold 2 s). Lifting is an inline confirmation in amber that says what
  follows: earlier access stays invalid, every agent must sign in again. The focus goes to
  the first button of the group.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import type { EmergencyStop } from '../../api/types.ts';
  import { formatDateTime, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    stop: EmergencyStop;
    ctx: FormatContext;
    /** Opens the emergency stop sheet of the frame. */
    ontrigger: () => void;
    onlift: () => Promise<void>;
  }

  let { stop, ctx, ontrigger, onlift }: Props = $props();

  const id = $props.id();
  let confirming = $state(false);
  let busy = $state(false);
  let failed = $state(false);
  let cancel: HTMLButtonElement | undefined = $state();
  let liftButton: HTMLButtonElement | undefined = $state();

  async function ask() {
    confirming = true;
    failed = false;
    await tick();
    cancel?.focus();
  }

  async function close() {
    confirming = false;
    await tick();
    liftButton?.focus();
  }

  async function lift() {
    if (busy) return;
    busy = true;
    failed = false;
    try {
      await onlift();
      confirming = false;
    } catch {
      failed = true;
    } finally {
      busy = false;
    }
  }
</script>

{#if stop.active}
  <p class="on">
    <Icon name="power" size={16} />{m.set_estop_on_desc({
      time: stop.since ? formatDateTime(new Date(stop.since), ctx) : '',
      person: isolate(stop.by_name ?? ''),
    })}
  </p>
  {#if confirming}
    <div class="confirm" role="group" aria-labelledby="{id}-title" aria-describedby="{id}-body">
      <h3 id="{id}-title">{m.set_estop_lift_title()}</h3>
      <p id="{id}-body">{m.set_estop_lift_body()}</p>
      <p class="error" role="alert">{#if failed}<Icon name="warning" size={16} />{m.set_save_failed()}{/if}</p>
      <div class="actions">
        <Button bind:element={cancel} disabled={busy} onclick={close}>{m.common_cancel()}</Button>
        <Button variant="primary" {busy} onclick={lift}>{m.set_estop_lift_confirm()}</Button>
      </div>
    </div>
  {:else}
    <Button bind:element={liftButton} onclick={ask}>{m.set_estop_lift()}</Button>
  {/if}
{:else}
  <p class="desc">{m.set_estop_off_desc()}</p>
  <Button variant="danger" icon="power" onclick={ontrigger}>{m.set_estop_trigger()}</Button>
{/if}

<style>
  .desc {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  .on {
    display: flex;
    gap: var(--hm-space-2);
    margin: 0;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-warning-bg);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  .confirm p {
    margin: 0;
  }
  .error {
    display: flex;
    gap: 6px;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
</style>
