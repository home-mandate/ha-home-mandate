<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Approvers (design README 6.11 section 1; decisions F2, F5, S1–S4): whether normal and
  critical requests reach anyone, one card per person, adding a person with a device (or,
  for an administrator, only the UI), the neutral note in Home Assistant's bell, and
  notifications in this browser for the signed-in person. Changes are saved at once; a
  failed save shows inline and the list goes back to the server's state.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError } from '../../api/client.ts';
  import type { ApproverList, ApproverUpdate } from '../../api/types.ts';
  import type { AppState } from '../../app/state.svelte.ts';
  import type { BrowserNotifier } from '../../app/notifier.svelte.ts';
  import { Loader } from '../../app/loader.svelte.ts';
  import { m } from '../../i18n.ts';
  import { isLastReachable, summary } from '../../settings/approvers.ts';
  import { toasts } from '../../ui/toasts.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Banner from '../Banner.svelte';
  import Button from '../Button.svelte';
  import ErrorState from '../ErrorState.svelte';
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

  /** Value of the "only in Home-Mandate" choice when adding a person. */
  const UI_ONLY = '';

  const list = new Loader<ApproverList>(() => app.api.approvers());
  let busy = $state<string | null>(null);
  let errors = $state<Record<string, string>>({});
  let person = $state('');
  let device = $state<string | null>(null);

  const reload = () => void list.run();
  onMount(() => {
    const stop = [app.on('approvers.changed', reload), app.on('reconnected', reload)];
    reload();
    return () => stop.forEach((off) => off());
  });

  const data = $derived(list.data);
  const overall = $derived(data ? summary(data.approvers) : null);
  const missing = $derived(data ? data.candidates.people.filter((p) => !data.approvers.some((a) => a.user_id === p.user_id)) : []);
  const chosen = $derived(missing.find((p) => p.user_id === person) ?? missing[0]);
  const deviceOptions = $derived([
    ...(data?.candidates.devices ?? []).map((d) => ({ value: d.service, label: cleanUntrusted(d.name) || d.service })),
    ...(chosen?.is_admin ? [{ value: UI_ONLY, label: m.set_approver_ui() }] : []),
  ]);
  const chosenDevice = $derived(deviceOptions.some((o) => o.value === device) ? device : (deviceOptions[0]?.value ?? null));
  const me = $derived(data?.approvers.find((a) => a.user_id === app.session?.user.id));

  function errorText(err: unknown): string {
    if (err instanceof ApiError && err.code === 'invalid_input') {
      if (err.field === '/ui' || err.field === '/ui_critical') return m.set_approver_error_ui();
      if (err.field === '/devices') return m.set_approver_error_devices();
    }
    return m.set_approver_save_failed();
  }

  async function run(userId: string, action: () => Promise<unknown>) {
    if (busy) return;
    busy = userId;
    errors = { ...errors, [userId]: '' };
    try {
      await action();
    } catch (err) {
      errors = { ...errors, [userId]: errorText(err) };
    } finally {
      busy = null;
      await list.run();
    }
  }

  const save = (userId: string, update: ApproverUpdate) => run(userId, () => app.api.putApprover(userId, update));
  const remove = (userId: string) => run(userId, () => app.api.deleteApprover(userId));

  async function test(userId: string, name: string) {
    try {
      await app.api.testApprover(userId);
      toasts.show({ kind: 'success', text: m.set_approver_test_sent({ person: isolate(name) }) });
    } catch {
      toasts.show({ kind: 'error', text: m.set_approver_test_failed() });
    }
  }

  function add() {
    const p = chosen;
    if (!p || !data || chosenDevice === null) return;
    const suggestion = data.candidates.devices.find((d) => d.service === chosenDevice);
    const update: ApproverUpdate = suggestion
      ? { devices: [{ service: suggestion.service, critical: suggestion.suggest_critical }], ui: false, ui_critical: false, language: null }
      : { devices: [], ui: true, ui_critical: false, language: null };
    person = '';
    device = null;
    void save(p.user_id, update);
  }
</script>

<p class="desc">{m.set_approvers_desc()}</p>

{#if list.status === 'error'}
  <ErrorState title={m.settings_error_title()} body={m.settings_error_body()} onretry={reload} />
{:else if !data}
  <Skeleton lines={['50%', '80%', '60%']} />
{:else}
  {#if overall === 'none'}
    <Banner kind="critical" body={m.set_approvers_min()} />
  {:else if overall === 'no_critical'}
    <Banner kind="warning" body={m.set_approvers_reach_no_critical()} />
  {:else}
    <p class="ok" role="status">{m.set_approvers_reach_ok()}</p>
  {/if}

  <div class="cards">
    {#each data.approvers as approver (approver.user_id)}
      <ApproverCard
        {approver}
        isAdmin={data.candidates.people.find((p) => p.user_id === approver.user_id)?.is_admin ?? false}
        candidates={data.candidates}
        last={approver.reach.normal && isLastReachable(data.approvers, approver.user_id)}
        busy={busy !== null}
        error={errors[approver.user_id] ?? ''}
        onsave={(u) => void save(approver.user_id, u)}
        ontest={() => void test(approver.user_id, approver.name)}
        onremove={() => void remove(approver.user_id)}
      />
    {/each}
  </div>

  <div class="add">
    {#if missing.length === 0}
      <p class="muted">{m.set_approver_add_none()}</p>
    {:else}
      <SelectField
        label={m.set_approver_add_label()}
        value={chosen?.user_id ?? ''}
        options={missing.map((p) => ({ value: p.user_id, label: cleanUntrusted(p.name) }))}
        onchange={(v) => (person = v)}
      />
      {#if deviceOptions.length > 0}
        <SelectField label={m.set_approver_device_label()} value={chosenDevice ?? ''} options={deviceOptions} onchange={(v) => (device = v)} />
        <Button icon="plus" busy={busy !== null && busy === chosen?.user_id} onclick={add}>{m.set_approver_add()}</Button>
      {:else}
        <p class="muted">{m.set_approver_no_device()}</p>
      {/if}
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
        <span role="status">{m.set_browser_notify_on()}</span>
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
    flex-wrap: wrap;
    align-items: flex-end;
    gap: var(--hm-space-3);
  }
  .add > :global(.field) {
    flex: 1 1 200px;
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
