<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One approver (decisions F2, S1, S2): reach as the server reports it, devices each with
  "critical requests too", answering in Home-Mandate (administrators only) with critical
  requests as a separate, warning switch, notification language, a test, and removal.
  A change is handed to the caller as a function of the approver, so it applies to the
  server's latest state even when several changes queue up. Removing the last reachable
  person asks inline first (amber, decision F5), and so does switching critical requests on
  in the UI or on a device without that suggestion, or a change after which critical
  requests reach nobody (decision S10). Device names come from the app's user:
  cut short and shown with their technical name, and with their owner when it is not
  this person.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import type { Approver, ApproverCandidates, ApproverUpdate, Language } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import {
    MAX_DEVICES,
    canGetCritical,
    freeDevices,
    suggestedCritical,
    updateOf,
    withDevice,
    withDeviceCritical,
    withoutDevice,
    withUi,
  } from '../../settings/approvers.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import IconButton from '../IconButton.svelte';
  import SelectField from '../SelectField.svelte';
  import StatusPill from '../StatusPill.svelte';
  import Switch from '../Switch.svelte';

  type Change = (u: ApproverUpdate) => ApproverUpdate;

  interface Props {
    approver: Approver;
    isAdmin: boolean;
    candidates: ApproverCandidates;
    /** Names of the people, for the owner of someone else's device. */
    people: ReadonlyMap<string, string>;
    /** Removing this person leaves nobody who gets requests. */
    last: boolean;
    /** This person is the only one critical requests reach. */
    lastCritical: boolean;
    error: string;
    onchange: (change: Change) => void;
    ontest: () => Promise<void>;
    onremove: () => void;
  }

  let { approver, isAdmin, candidates, people, last, lastCritical, error, onchange, ontest, onremove }: Props = $props();

  const id = $props.id();
  /** Longest device name shown: the app's user chooses it. */
  const DEVICE_NAME_MAX = 60;

  interface Pending {
    text: string;
    confirm: string;
    danger: boolean;
    apply: () => void;
    /** Where the focus goes back on Cancel. */
    from: HTMLElement | null;
  }

  let adding = $state('');
  let pending: Pending | null = $state(null);
  let testing = $state(false);
  let confirmCancel: HTMLButtonElement | undefined = $state();

  const name = $derived(cleanUntrusted(approver.name));
  const update = $derived(updateOf(approver));
  const free = $derived(freeDevices(update, candidates));
  const full = $derived(update.devices.length >= MAX_DEVICES);
  const uiInactive = $derived(update.ui && !isAdmin);
  const reach = $derived.by(() => {
    const { normal, critical } = approver.reach;
    if (normal === 'none') return { tone: 'danger' as const, text: m.set_reach_none() };
    if (normal === 'ui') return { tone: 'warning' as const, text: critical === 'ui' ? m.set_reach_ui() : m.set_reach_ui_normal() };
    if (critical === 'push') return { tone: 'positive' as const, text: m.set_reach_all() };
    return { tone: 'warning' as const, text: critical === 'ui' ? m.set_reach_push_critical_ui() : m.set_reach_normal() };
  });
  const languages = [
    { value: '', label: m.lang_auto() },
    { value: 'de', label: m.lang_de(), lang: 'de' },
    { value: 'en', label: m.lang_en(), lang: 'en' },
  ];



  function deviceLabel(service: string): { name: string; owner: string } {
    const d = candidates.devices.find((x) => x.service === service);
    const shown = cleanUntrusted(d?.name, DEVICE_NAME_MAX) || service;
    if (!d || d.owner_user_id === approver.user_id) return { name: shown, owner: '' };
    const owner = d.owner_user_id ? people.get(d.owner_user_id) : undefined;
    return { name: shown, owner: owner ? m.set_device_of({ person: isolate(owner) }) : m.set_device_unknown_owner() };
  }

  async function ask(text: string, confirm: string, apply: () => void, danger = true) {
    pending = { text, confirm, danger, apply, from: document.activeElement instanceof HTMLElement ? document.activeElement : null };
    await tick();
    confirmCancel?.focus();
  }

  async function cancel() {
    const from = pending?.from;
    pending = null;
    await tick();
    if (from?.isConnected) from.focus();
  }

  function confirmPending() {
    const apply = pending?.apply;
    pending = null;
    apply?.();
  }

  /**
   * change hands an edit on, after a confirmation when it switches critical requests on
   * (enabling: the warning) or when afterwards critical requests would reach nobody.
   */
  function change(edit: Change, enabling?: string) {
    if (enabling) {
      void ask(enabling, m.set_confirm_enable(), () => onchange(edit));
      return;
    }
    if (lastCritical && canGetCritical(update) && !canGetCritical(edit(update))) {
      void ask(m.set_confirm_last_critical(), m.set_confirm_change(), () => onchange(edit));
      return;
    }
    onchange(edit);
  }

  function deviceCritical(service: string, on: boolean) {
    const candidate = candidates.devices.find((d) => d.service === service);
    const suggested = candidate !== undefined && suggestedCritical(candidate, approver.user_id);
    const warning = on && !suggested ? m.set_confirm_critical_device({ device: isolate(deviceLabel(service).name) }) : undefined;
    change((u) => withDeviceCritical(u, service, on), warning);
  }

  function add() {
    const device = free.find((d) => d.service === adding) ?? free[0];
    if (!device || full) return;
    adding = '';
    onchange((u) => (u.devices.some((d) => d.service === device.service) ? u : withDevice(u, device, approver.user_id)));
  }

  async function test() {
    if (testing) return;
    testing = true;
    try {
      await ontest();
    } finally {
      testing = false;
    }
  }

  function remove() {
    if (last) void ask(m.set_approver_remove_last({ person: isolate(name) }), m.set_approver_remove_confirm(), onremove);
    else if (lastCritical) void ask(m.set_approver_remove_last_critical({ person: isolate(name) }), m.set_approver_remove_confirm(), onremove);
    else onremove();
  }
</script>

<article aria-labelledby="{id}-name">
  <div class="head">
    <h3 id="{id}-name"><bdi>{name}</bdi></h3>
    <StatusPill tone={reach.tone} shape={reach.tone === 'positive' ? 'dot' : 'ring'}>{reach.text}</StatusPill>
  </div>

  <!-- Every control below is about this person; the group carries the name. -->
  <div class="body" role="group" aria-labelledby="{id}-name">
    <fieldset>
      <legend>{m.set_approver_devices()}</legend>
      <ul role="list">
        {#each update.devices as device (device.service)}
          {@const label = deviceLabel(device.service)}
          <li>
            <div class="device" role="group" aria-labelledby="{id}-{device.service}">
              <span class="device-name" id="{id}-{device.service}"
                ><bdi>{label.name}</bdi>{#if label.owner}<span class="owner">{label.owner}</span>{/if}</span
              >
              <span class="service" dir="ltr">{device.service}</span>
              <Switch
                checked={device.critical}
                label={m.set_approver_device_critical()}
                onchange={(on) => deviceCritical(device.service, on)}
              />
              <IconButton
                icon="close"
                label={m.set_approver_device_remove({ device: isolate(label.name) })}
                onclick={() => change((u) => withoutDevice(u, device.service))}
              />
            </div>
          </li>
        {/each}
      </ul>
      {#if free.length > 0}
        <div class="add">
          <SelectField
            label={m.set_approver_device_label()}
            value={adding || (free[0]?.service ?? '')}
            options={free.map((d) => {
              const l = deviceLabel(d.service);
              return { value: d.service, label: l.owner ? `${l.name} · ${l.owner}` : l.name };
            })}
            help={full ? m.set_approver_device_max({ max: MAX_DEVICES }) : undefined}
            onchange={(v) => (adding = v)}
          />
          <Button icon="plus" disabled={full} onclick={add}>{m.set_approver_device_add()}</Button>
        </div>
      {/if}
      <p class="help">{m.set_approver_device_critical_help()}</p>
    </fieldset>

    <div class="switches">
      <Switch
        checked={update.ui}
        label={m.set_approver_ui()}
        description={uiInactive ? m.set_approver_ui_inactive() : isAdmin ? m.set_approver_ui_desc() : m.set_approver_ui_admin()}
        disabled={!isAdmin && !update.ui}
        onchange={(on) => change((u) => withUi(u, on))}
      />
      <Switch
        checked={update.ui_critical}
        tone="danger"
        label={m.set_approver_ui_critical()}
        description={update.ui ? m.set_approver_ui_critical_desc() : m.set_approver_ui_critical_needs_ui()}
        disabled={!update.ui}
        onchange={(on) => change((u) => ({ ...u, ui_critical: on && u.ui }), on ? m.set_confirm_critical_ui() : undefined)}
      />
    </div>

    <SelectField
      label={m.set_approvers_lang()}
      value={update.language ?? ''}
      options={languages}
      onchange={(v) => onchange((u) => ({ ...u, language: v === 'de' || v === 'en' ? (v as Language) : null }))}
    />

    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>

    {#if pending}
      <div class="confirm" role="group" aria-labelledby="{id}-confirm">
        <p id="{id}-confirm">{pending.text}</p>
        <div class="actions">
          <Button bind:element={confirmCancel} onclick={cancel}>{m.common_cancel()}</Button>
          <Button variant={pending.danger ? 'danger' : 'primary'} onclick={confirmPending}>{pending.confirm}</Button>
        </div>
      </div>
    {:else}
      <div class="actions">
        <Button busy={testing} disabled={update.devices.length === 0} aria-describedby={update.devices.length === 0 ? `${id}-test` : undefined} onclick={test}
          >{m.set_approver_test()}</Button
        >
        {#if update.devices.length === 0}<span id="{id}-test" class="help">{m.set_approver_test_needs_device()}</span>{/if}
        <Button variant="text" onclick={remove}>{m.set_approver_remove({ person: isolate(name) })}</Button>
      </div>
    {/if}
  </div>
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
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
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
  .device {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2) var(--hm-space-4);
    padding: var(--hm-space-2) var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
  }
  .device-name {
    display: inline-flex;
    flex-direction: column;
    flex: 1 1 160px;
    font-weight: var(--hm-font-weight-semibold);
    overflow-wrap: anywhere;
  }
  .owner {
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-regular);
    color: var(--hm-color-warning-fg);
  }
  .service {
    font-family: var(--hm-font-mono);
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
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
    color: var(--hm-color-text-muted);
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
    align-items: center;
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
