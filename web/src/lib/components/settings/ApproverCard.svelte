<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One approver (decisions F2, S1, S2): reach as the server reports it, devices each with
  "critical requests too", answering in Home-Mandate (administrators only) with critical
  requests as a separate, warning switch, notification language, a test, and removal.
  Every change is saved at once by the caller; removing the last reachable person asks
  inline first (amber, decision F5).
-->
<script lang="ts">
  import type { Approver, ApproverCandidates, ApproverUpdate, Language } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { MAX_DEVICES, freeDevices, updateOf, withDevice, withDeviceCritical, withoutDevice, withUi } from '../../settings/approvers.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import IconButton from '../IconButton.svelte';
  import SelectField from '../SelectField.svelte';
  import StatusPill from '../StatusPill.svelte';
  import Switch from '../Switch.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    approver: Approver;
    isAdmin: boolean;
    candidates: ApproverCandidates;
    /** Removing this person leaves nobody who gets requests. */
    last: boolean;
    busy: boolean;
    error: string;
    onsave: (update: ApproverUpdate) => void;
    ontest: () => void;
    onremove: () => void;
  }

  let { approver, isAdmin, candidates, last, busy, error, onsave, ontest, onremove }: Props = $props();

  const id = $props.id();
  let adding = $state('');
  let confirming = $state(false);
  let confirmButton: HTMLButtonElement | undefined = $state();

  const name = $derived(cleanUntrusted(approver.name));
  const update = $derived(updateOf(approver));
  const free = $derived(freeDevices(update, candidates));
  const full = $derived(update.devices.length >= MAX_DEVICES);
  const deviceName = (service: string) => cleanUntrusted(candidates.devices.find((d) => d.service === service)?.name) || service;
  const reach = $derived(
    approver.reach.critical
      ? { tone: 'positive' as const, text: m.set_reach_all() }
      : approver.reach.normal
        ? { tone: 'warning' as const, text: m.set_reach_normal() }
        : { tone: 'danger' as const, text: m.set_reach_none() },
  );
  const languages = $derived([
    { value: '', label: m.lang_auto() },
    { value: 'de', label: m.lang_de() },
    { value: 'en', label: m.lang_en() },
  ]);

  function add() {
    const device = free.find((d) => d.service === adding) ?? free[0];
    if (!device || full || busy) return;
    adding = '';
    onsave(withDevice(update, device));
  }

  async function remove() {
    if (busy) return;
    if (!last || confirming) {
      confirming = false;
      onremove();
      return;
    }
    confirming = true;
    await Promise.resolve();
    confirmButton?.focus();
  }
</script>

<article aria-labelledby="{id}-name">
  <div class="head">
    <h3 id="{id}-name"><bdi>{name}</bdi></h3>
    <StatusPill tone={reach.tone} shape={reach.tone === 'positive' ? 'dot' : 'ring'}>{reach.text}</StatusPill>
  </div>

  <fieldset>
    <legend>{m.set_approver_devices()}</legend>
    <ul role="list">
      {#each update.devices as device (device.service)}
        <li role="group" aria-labelledby="{id}-{device.service}">
          <span class="device" id="{id}-{device.service}"><bdi>{deviceName(device.service)}</bdi></span>
          <Switch
            checked={device.critical}
            label={m.set_approver_device_critical()}
            disabled={busy}
            onchange={(on) => onsave(withDeviceCritical(update, device.service, on))}
          />
          <IconButton
            icon="close"
            label={m.set_approver_device_remove({ device: isolate(deviceName(device.service)) })}
            onclick={() => !busy && onsave(withoutDevice(update, device.service))}
          />
        </li>
      {/each}
    </ul>
    {#if free.length > 0}
      <div class="add">
        <SelectField
          label={m.set_approver_device_label()}
          value={adding || (free[0]?.service ?? '')}
          options={free.map((d) => ({ value: d.service, label: cleanUntrusted(d.name) || d.service }))}
          help={full ? m.set_approver_device_max({ max: MAX_DEVICES }) : undefined}
          disabled={full || busy}
          onchange={(v) => (adding = v)}
        />
        <Button icon="plus" disabled={full || busy} onclick={add}>{m.set_approver_device_add()}</Button>
      </div>
    {/if}
    <p class="help">{m.set_approver_device_critical_help()}</p>
  </fieldset>

  <div class="switches">
    <Switch
      checked={update.ui}
      label={m.set_approver_ui()}
      description={isAdmin ? m.set_approver_ui_desc() : m.set_approver_ui_admin()}
      disabled={busy || (!isAdmin && !update.ui)}
      onchange={(on) => onsave(withUi(update, on))}
    />
    <Switch
      checked={update.ui_critical}
      tone="danger"
      label={m.set_approver_ui_critical()}
      description={m.set_approver_ui_critical_desc()}
      disabled={busy || !update.ui}
      onchange={(on) => onsave({ ...update, ui_critical: on })}
    />
  </div>

  <SelectField
    label={m.set_approvers_lang()}
    value={update.language ?? ''}
    options={languages}
    disabled={busy}
    onchange={(v) => onsave({ ...update, language: (v || null) as Language | null })}
  />

  <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>

  {#if confirming}
    <div class="confirm" role="group" aria-labelledby="{id}-confirm">
      <p id="{id}-confirm">{m.set_approver_remove_last({ person: isolate(name) })}</p>
      <div class="actions">
        <Button bind:element={confirmButton} onclick={() => (confirming = false)}>{m.common_cancel()}</Button>
        <Button variant="danger" {busy} onclick={remove}>{m.set_approver_remove_confirm()}</Button>
      </div>
    </div>
  {:else}
    <div class="actions">
      <Button disabled={busy || update.devices.length === 0} onclick={ontest}>{m.set_approver_test()}</Button>
      <Button variant="text" disabled={busy} onclick={remove}>{m.set_approver_remove({ person: isolate(name) })}</Button>
    </div>
  {/if}
</article>

<style>
  article {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    padding: var(--hm-space-5);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--hm-space-2);
  }
  /* The reach is a sentence: it wraps on narrow screens instead of widening the page. */
  .head :global(.pill) {
    white-space: normal;
    min-inline-size: 0;
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  fieldset {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    margin: 0;
    padding: 0;
    border: 0;
    min-inline-size: 0;
  }
  legend {
    margin-block-end: var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
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
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-4);
    padding: var(--hm-space-2) var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
  }
  .device {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    flex: 1 1 160px;
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .add {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: var(--hm-space-3);
  }
  .add > :global(:first-child) {
    flex: 1 1 200px;
  }
  .help {
    margin: 0;
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .switches {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  .error {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
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
</style>
