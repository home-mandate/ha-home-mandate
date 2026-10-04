<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Critical devices (SPEC-v0 section 4, step 5): the devices the household marked, the
  proposals of the server and a search for any other device. Marking saves at once;
  removing a mark lowers the protection, so it asks inline first. Changes run one after
  another and the list is reloaded after each; a device that moves between the lists keeps
  the focus on its switch.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ApiError } from '../../api/client.ts';
  import type { Device, DeviceCatalog } from '../../api/types.ts';
  import { Loader } from '../../app/loader.svelte.ts';
  import type { AppState } from '../../app/state.svelte.ts';
  import { m } from '../../i18n.ts';
  import { criticalGroups } from '../../settings/critical.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import ErrorState from '../ErrorState.svelte';
  import Icon from '../Icon.svelte';
  import Skeleton from '../Skeleton.svelte';
  import Switch from '../Switch.svelte';
  import TextField from '../TextField.svelte';

  interface Props {
    app: AppState;
  }

  let { app }: Props = $props();

  const id = $props.id();
  const list = new Loader<DeviceCatalog>(() => app.api.devices());
  let queue: Promise<void> = Promise.resolve();
  let query = $state('');
  let error = $state('');
  let announcement = $state('');
  let pending = $state<{ device: Device; from: HTMLElement | null } | null>(null);
  let confirmCancel: HTMLButtonElement | undefined = $state();
  let root: HTMLElement | undefined = $state();
  let markedHeading: HTMLElement | undefined = $state();

  const reload = () => void list.run();
  onMount(() => {
    const stop = [app.on('devices.changed', reload), app.on('reconnected', reload)];
    reload();
    return () => stop.forEach((off) => off());
  });

  const groups = $derived(list.data ? criticalGroups(list.data, query) : null);
  const areas = $derived(new Map((list.data?.areas ?? []).map((a) => [a.id, cleanUntrusted(a.name)])));

  const nameOf = (d: Device) => cleanUntrusted(d.name) || d.entity_id;
  const detailOf = (d: Device) => {
    const area = d.area ? areas.get(d.area) : undefined;
    const entity = cleanUntrusted(d.entity_id);
    return area ? `${entity} · ${area}` : entity;
  };

  /**
   * focusSwitch moves the focus to the switch of a device wherever it is listed now; a
   * device that left every list hands it to the heading of the marked ones.
   */
  async function focusSwitch(entityId: string) {
    await tick();
    const row = root?.querySelector(`[data-entity="${CSS.escape(entityId)}"]`);
    const target = row?.querySelector<HTMLElement>('[role="switch"]') ?? markedHeading;
    target?.focus();
  }

  // A mark removed elsewhere while its confirmation is open: nothing left to confirm.
  $effect(() => {
    const device = pending?.device;
    if (device && list.data && !list.data.devices.some((d) => d.entity_id === device.entity_id && d.critical)) pending = null;
  });

  function save(device: Device, critical: boolean, refocus: boolean) {
    error = '';
    const name = isolate(nameOf(device));
    queue = queue.then(async () => {
      try {
        await app.api.putDeviceCritical(device.entity_id, critical);
        // Cleared first, so the same sentence twice in a row is read again.
        announcement = '';
        await tick();
        announcement = critical ? m.set_critical_marked_done({ device: name }) : m.set_critical_unmarked_done({ device: name });
      } catch (err) {
        error = err instanceof ApiError && err.code === 'not_found' ? m.set_critical_gone({ device: name }) : m.set_save_failed();
      }
      await list.run();
      if (refocus) await focusSwitch(device.entity_id);
    });
  }

  async function toggle(device: Device, on: boolean) {
    if (on) {
      save(device, true, true);
      return;
    }
    pending = { device, from: document.activeElement instanceof HTMLElement ? document.activeElement : null };
    await tick();
    confirmCancel?.focus();
  }

  async function cancel() {
    const from = pending?.from;
    pending = null;
    await tick();
    if (from?.isConnected) from.focus();
  }

  function confirmRemove() {
    const device = pending?.device;
    pending = null;
    if (device) save(device, false, true);
  }
</script>

{#snippet rows(devices: Device[])}
  <ul role="list">
    {#each devices as d (d.entity_id)}
      <li data-entity={d.entity_id}>
        <Switch checked={d.critical} label={nameOf(d)} description={detailOf(d)} onchange={(on) => void toggle(d, on)} />
      </li>
    {/each}
  </ul>
{/snippet}

<div class="critical" bind:this={root}>
  <p class="desc">{m.set_critical_desc()}</p>

  {#if list.status === 'error'}
    <ErrorState title={m.set_critical_error_title()} body={m.set_critical_error_body()} onretry={reload} />
  {:else if !groups}
    <Skeleton lines={['60%', '40%']} />
  {:else}
    <div class="group">
      <h3 bind:this={markedHeading} tabindex="-1">{m.set_critical_marked()}</h3>
      {#if groups.marked.length === 0}
        <p class="muted">{m.set_critical_none()}</p>
      {:else}
        {@render rows(groups.marked)}
      {/if}
      {#if pending}
        <div class="confirm" role="group" aria-labelledby="{id}-confirm">
          <p id="{id}-confirm">{m.set_critical_remove_text({ device: isolate(nameOf(pending.device)) })}</p>
          <div class="actions">
            <Button bind:element={confirmCancel} onclick={cancel}>{m.common_cancel()}</Button>
            <Button variant="danger" onclick={confirmRemove}>{m.set_critical_remove_confirm()}</Button>
          </div>
        </div>
      {/if}

      <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    </div>

    {#if groups.suggested.length > 0}
      <div class="group">
        <h3>{m.set_critical_suggested()}</h3>
        <p class="muted">{m.set_critical_suggested_desc()}</p>
        {@render rows(groups.suggested)}
      </div>
    {/if}

    <div class="group">
      <TextField type="search" label={m.set_critical_search()} help={m.set_critical_search_help()} bind:value={query} autocomplete="off" spellcheck="false" />
      {#if query.trim() !== ''}
        {#if groups.matches.length === 0}
          <p class="muted">{m.set_critical_no_match()}</p>
        {:else}
          {@render rows(groups.matches)}
          {#if groups.more > 0}<p class="muted">{m.set_critical_more({ count: groups.more })}</p>{/if}
        {/if}
      {/if}
    </div>
  {/if}

  <p class="hm-visually-hidden" role="status">{announcement}</p>
</div>

<style>
  .critical,
  .group {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .critical {
    gap: var(--hm-space-5);
  }
  .desc,
  .muted {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  h3 {
    margin: 0;
    outline: none;
    font-size: var(--hm-font-size-md);
    font-weight: var(--hm-font-weight-semibold);
  }
  ul {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    min-inline-size: 0;
    overflow-wrap: anywhere;
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
  .confirm p {
    margin: 0;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
  .error {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    margin: 0;
    color: var(--hm-color-danger-fg);
  }
  .error:empty {
    display: none;
  }
</style>
