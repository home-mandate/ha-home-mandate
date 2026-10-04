<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Approvers (design README 6.11 section 1; decisions F2, F5, S1–S4): whether normal and
  critical requests reach anyone, one card per person, adding a person, the neutral note in
  Home Assistant's bell, and notifications in this browser for the signed-in person.
  Changes run one after another, each on the state the server answered last, so quick
  clicks never undo each other; controls stay usable meanwhile. A refused change is said
  where it happened and the server's state shows again; a saved one is announced.
  Adding a person needs an explicit device choice when none of the devices is theirs, so
  nobody gets someone else's phone by default.
-->
<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ApiError } from '../../api/client.ts';
  import type { ApproverList, ApproverUpdate } from '../../api/types.ts';
  import type { AppState } from '../../app/state.svelte.ts';
  import type { BrowserNotifier } from '../../app/notifier.svelte.ts';
  import { Loader } from '../../app/loader.svelte.ts';
  import { m } from '../../i18n.ts';
  import { isLastCritical, isLastReachable, newApprover, ownDevices, summary, updateOf } from '../../settings/approvers.ts';
  import { toasts } from '../../ui/toasts.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Banner from '../Banner.svelte';
  import Button from '../Button.svelte';
  import ErrorState from '../ErrorState.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import Skeleton from '../Skeleton.svelte';
  import Switch from '../Switch.svelte';
  import ApproverCard from './ApproverCard.svelte';

  interface Props {
    app: AppState;
    notifier: BrowserNotifier;
    /** The bell switch from the defaults; null while they load. */
    bell: boolean | null;
    onbell: (on: boolean) => void;
  }

  let { app, notifier, bell, onbell }: Props = $props();

  /** Device choice "only in Home-Mandate" (administrators) and "nothing chosen yet". */
  const UI_ONLY = 'ui';
  const NONE = '';
  const DEVICE_NAME_MAX = 60;

  const id = $props.id();
  const list = new Loader<ApproverList>(() => app.api.approvers());
  let queue: Promise<void> = Promise.resolve();
  let errors = $state<Record<string, string>>({});
  let addError = $state('');
  let announcement = $state('');
  let person = $state('');
  let device = $state<string | null>(null);
  let adding = $state(false);
  let personSelect: HTMLElement | undefined = $state();

  const reload = () => void list.run();
  onMount(() => {
    const stop = [app.on('approvers.changed', reload), app.on('reconnected', reload)];
    reload();
    return () => stop.forEach((off) => off());
  });

  const data = $derived(list.data);
  const overall = $derived(data ? summary(data.approvers) : null);
  const people = $derived(new Map((data?.candidates.people ?? []).map((p) => [p.user_id, cleanUntrusted(p.name)])));
  const missing = $derived(data ? data.candidates.people.filter((p) => !data.approvers.some((a) => a.user_id === p.user_id)) : []);
  const chosen = $derived(missing.find((p) => p.user_id === person) ?? missing[0]);
  const deviceOptions = $derived.by(() => {
    if (!data || !chosen) return [];
    const own = ownDevices(data.candidates.devices, chosen.user_id);
    const others = data.candidates.devices.filter((d) => !own.includes(d));
    const label = (d: (typeof others)[number]) => cleanUntrusted(d.name, DEVICE_NAME_MAX) || d.service;
    const ownerOf = (d: (typeof others)[number]) => {
      const owner = d.owner_user_id ? people.get(d.owner_user_id) : undefined;
      return owner ? m.set_device_of({ person: isolate(owner) }) : m.set_device_unknown_owner();
    };
    return [
      // Without an own device the person must choose: nothing is preselected.
      ...(own.length === 0 && !chosen.is_admin ? [{ value: NONE, label: m.set_approver_device_label() + ' …' }] : []),
      ...own.map((d) => ({ value: d.service, label: label(d) })),
      ...(chosen.is_admin ? [{ value: UI_ONLY, label: m.set_approver_ui() }] : []),
      ...others.map((d) => ({ value: d.service, label: `${label(d)} · ${ownerOf(d)}` })),
    ];
  });
  const chosenDevice = $derived(deviceOptions.some((o) => o.value === device) ? (device as string) : (deviceOptions[0]?.value ?? NONE));
  const me = $derived(data?.approvers.find((a) => a.user_id === app.session?.user.id));

  function errorText(err: unknown, name: string): string {
    // The first change wins: someone else's came first; the list is reloaded after this.
    if (err instanceof ApiError && err.code === 'conflict') return m.set_approver_conflict({ person: isolate(name) });
    if (err instanceof ApiError && err.code === 'invalid_input') {
      if (err.field === '/ui' || err.field === '/ui_critical') return m.set_approver_error_ui();
      if (err.field === '/devices') return m.set_approver_error_devices();
    }
    return m.set_approver_save_failed();
  }

  /** enqueue runs an action after the ones before it; the list is reloaded before the next starts. */
  function enqueue(name: string, action: () => Promise<string>, onerror: (text: string) => void): Promise<void> {
    queue = queue.then(async () => {
      try {
        announcement = await action();
      } catch (err) {
        onerror(errorText(err, name));
      }
      await list.run();
    });
    return queue;
  }

  function change(userId: string, name: string, edit: (u: ApproverUpdate) => ApproverUpdate) {
    errors = { ...errors, [userId]: '' };
    void enqueue(
      name,
      async () => {
        // The latest state the server gave, not the one the click saw; its version is the base.
        const current = list.data?.approvers.find((a) => a.user_id === userId);
        if (!current || !list.data) throw new Error('gone');
        await app.api.putApprover(userId, edit(updateOf(current)), list.data.version);
        return m.set_approver_saved({ person: isolate(name) });
      },
      (text) => (errors = { ...errors, [userId]: text }),
    );
  }

  async function remove(userId: string, name: string) {
    await enqueue(
      name,
      async () => {
        if (!list.data) throw new Error('gone');
        await app.api.deleteApprover(userId, list.data.version);
        return m.set_approver_removed({ person: isolate(name) });
      },
      (text) => (errors = { ...errors, [userId]: text }),
    );
    await tick();
    // The card is gone: the focus goes to adding a person.
    personSelect?.querySelector('select')?.focus();
  }

  async function test(userId: string, name: string) {
    try {
      await app.api.testApprover(userId);
      toasts.show({ kind: 'success', text: m.set_approver_test_sent({ person: isolate(name) }) });
    } catch {
      toasts.show({ kind: 'error', text: m.set_approver_test_failed() });
    }
  }

  async function add() {
    const p = chosen;
    if (!p || !data || chosenDevice === NONE || adding) return;
    const candidate = data.candidates.devices.find((d) => d.service === chosenDevice) ?? null;
    const update = newApprover(p.user_id, chosenDevice === UI_ONLY ? null : candidate);
    adding = true;
    addError = '';
    let ok = false;
    await enqueue(
      cleanUntrusted(p.name),
      async () => {
        if (!list.data) throw new Error('gone');
        await app.api.putApprover(p.user_id, update, list.data.version);
        ok = true;
        return m.set_approver_added({ person: isolate(cleanUntrusted(p.name)) });
      },
      (text) => (addError = text),
    );
    adding = false;
    if (ok) {
      person = '';
      device = null;
    }
  }
</script>

<p class="hm-visually-hidden" role="status">{announcement}</p>
<p class="desc">{m.set_approvers_desc()}</p>

{#if list.status === 'error' && !data}
  <ErrorState title={m.settings_error_title()} body={m.settings_error_body()} onretry={reload} />
{:else if !data}
  <Skeleton lines={['50%', '80%', '60%']} />
{:else}
  {#if overall === 'none'}
    <Banner kind="critical" quiet body={m.set_approvers_min()} />
  {:else if overall === 'ui_only'}
    <Banner kind="warning" quiet body={m.set_approvers_reach_ui_only()} />
  {:else if overall === 'no_critical'}
    <Banner kind="warning" quiet body={m.set_approvers_reach_no_critical()} />
  {:else if overall === 'critical_ui_only'}
    <Banner kind="warning" quiet body={m.set_approvers_reach_critical_ui()} />
  {:else}
    <p class="ok">{m.set_approvers_reach_ok()}</p>
  {/if}

  <div class="cards">
    {#each data.approvers as approver (approver.user_id)}
      <ApproverCard
        {approver}
        isAdmin={data.candidates.people.find((p) => p.user_id === approver.user_id)?.is_admin ?? false}
        candidates={data.candidates}
        {people}
        last={approver.reach.normal !== 'none' && isLastReachable(data.approvers, approver.user_id)}
        lastCritical={isLastCritical(data.approvers, approver.user_id)}
        error={errors[approver.user_id] ?? ''}
        onchange={(edit) => change(approver.user_id, approver.name, edit)}
        ontest={() => test(approver.user_id, approver.name)}
        onremove={() => void remove(approver.user_id, approver.name)}
      />
    {/each}
  </div>

  <div class="add" role="group" aria-labelledby="{id}-add">
    <span id="{id}-add" class="hm-visually-hidden">{m.set_approver_add()}</span>
    {#if missing.length === 0}
      <p class="muted">{m.set_approver_add_none()}</p>
    {:else}
      <div class="add-row" bind:this={personSelect}>
        <SelectField
          label={m.set_approver_add_label()}
          value={chosen?.user_id ?? ''}
          options={missing.map((p) => ({ value: p.user_id, label: cleanUntrusted(p.name) }))}
          onchange={(v) => (person = v)}
        />
        {#if deviceOptions.length > 0}
          <SelectField label={m.set_approver_device_label()} value={chosenDevice} options={deviceOptions} onchange={(v) => (device = v)} />
          <Button icon="plus" busy={adding} disabled={chosenDevice === NONE} onclick={add}>{m.set_approver_add()}</Button>
        {:else}
          <p class="muted">{m.set_approver_no_device()}</p>
        {/if}
      </div>
      <p class="error" role="alert">{#if addError}<Icon name="warning" size={16} />{addError}{/if}</p>
    {/if}
  </div>
{/if}

<div class="extra">
  <Switch checked={bell ?? false} label={m.set_bell()} description={m.set_bell_desc()} disabled={bell === null} onchange={onbell} />

  {#if me?.ui}
    <div class="notify">
      <span class="label">{m.set_browser_notify()}</span>
      <span class="muted">{m.set_browser_notify_desc()}</span>
      {#if notifier.state === 'unsupported'}
        <span class="muted">{m.set_browser_notify_unsupported()}</span>
      {:else if notifier.state === 'blocked'}
        <span class="muted">{m.set_browser_notify_denied()}</span>
      {:else if notifier.state === 'on'}
        <span>{m.set_browser_notify_on()}</span>
        <Button variant="text" onclick={() => notifier.disable()}>{m.set_browser_notify_off()}</Button>
      {:else}
        <Button onclick={() => void notifier.enable()}>{m.set_browser_notify_allow()}</Button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .desc,
  .ok,
  .muted {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
  .ok {
    color: var(--hm-color-positive-fg);
    font-weight: var(--hm-font-weight-medium);
  }
  .cards {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  .add {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .add-row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: var(--hm-space-3);
  }
  .add-row > :global(.field) {
    flex: 1 1 200px;
  }
  .error {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
  .extra {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    padding-block-start: var(--hm-space-4);
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .notify {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-2);
  }
  .label {
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
  }
</style>
