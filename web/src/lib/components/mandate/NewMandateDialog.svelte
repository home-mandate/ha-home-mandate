<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  New mandate: a copy of a template for an active agent that has no active mandate (a
  mandate always belongs to one agent, decision D3). The server checks again; a conflict
  means someone else was faster.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { Agent, DeviceCatalog, Rule, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { getLocale } from '../../paraglide/runtime.js';
  import { NAME_MAX } from '../../mandate/problems.ts';
  import { templateDescription, templateTitle, titleOf } from '../../mandate/template.ts';
  import { href } from '../../router.ts';
  import { clientIdentity } from '../../ui/identity.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import TextField from '../TextField.svelte';
  import CriticalTemplateConfirm from './CriticalTemplateConfirm.svelte';
  import PlainWords from './PlainWords.svelte';

  interface Props {
    open: boolean;
    api: ApiClient;
    /** Active agents without an active mandate. */
    agents: readonly Agent[];
    /** Templates that are offered (no hidden base templates). */
    templates: readonly Template[];
    catalog: DeviceCatalog;
    /** Template chosen on the list; the first one otherwise. */
    template: string | null;
    onclose: () => void;
    oncreated: (id: string) => void;
  }

  let { open, api, agents, templates, catalog, template, onclose, oncreated }: Props = $props();

  const id = $props.id();
  let agent = $state('');
  let chosen = $state('');
  let name = $state('');
  let named = $state(false);
  let checked = $state(false);
  let busy = $state(false);
  let error = $state('');
  /** Rules of the template that need the separate confirmation (U9), once the server asked. */
  let confirming: Rule[] | null = $state(null);
  let nameField: HTMLInputElement | undefined = $state();

  $effect(() => {
    if (!open) return;
    untrack(() => {
      agent = agents[0]?.client_id ?? '';
      chosen = template ?? templates[0]?.name ?? '';
      name = titleOf(chosen, templates);
      named = false;
      checked = false;
      busy = false;
      error = '';
      confirming = null;
    });
  });

  const agentOptions = $derived(
    agents.map((a) => {
      const identity = clientIdentity(a.oauth_client);
      const label = cleanUntrusted(a.display_name);
      return { value: a.client_id, label: identity ? `${label} · ${identity.text}` : label };
    }),
  );
  const templateOptions = $derived(templates.map((t) => ({ value: t.name, label: templateTitle(t) })));
  const picked = $derived(templates.find((t) => t.name === chosen) ?? null);
  const length = $derived([...name.trim()].length);
  const nameError = $derived(checked && (length < 1 || length > NAME_MAX) ? m.validation_name({ max: NAME_MAX }) : '');
  const possible = $derived(agents.length > 0 && templates.length > 0);

  function pickTemplate(next: string) {
    confirming = null; // another template: its own confirmation
    if (!named) name = titleOf(next, templates);
  }

  const agentName = $derived(agents.find((a) => a.client_id === agent)?.display_name ?? '');

  function failure(err: unknown): string {
    if (!(err instanceof ApiError)) return m.mandates_new_failed();
    if (err.code === 'conflict') return m.mandates_new_conflict();
    if (err.code === 'no_approvers') return m.template_no_approvers();
    // A template hidden or removed meanwhile.
    if (err.code === 'invalid_input' && err.field === '/template') return m.template_gone();
    return m.mandates_new_failed();
  }

  async function create(event: SubmitEvent) {
    event.preventDefault();
    await send(false);
  }

  async function send(confirm: boolean) {
    checked = true;
    if (possible && (length < 1 || length > NAME_MAX)) {
      await tick();
      nameField?.focus();
      return;
    }
    if (busy || !possible) return;
    busy = true;
    error = '';
    try {
      const created = await api.createMandate({ client_id: agent, template: chosen, name: name.trim(), ...(confirm ? { confirm_critical: true } : {}) });
      confirming = null;
      oncreated(created.summary.id);
    } catch (err) {
      if (!confirm && err instanceof ApiError && err.code === 'critical_confirmation_required') {
        confirming = templates.find((t) => t.name === chosen)?.draft.rules.filter((r) => r.allow_critical === true) ?? [];
        return;
      }
      confirming = null;
      error = failure(err);
    } finally {
      busy = false;
    }
  }
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" {onclose}>
  <form onsubmit={create} novalidate>
    <h2 id="{id}-title">{m.mandates_new()}</h2>
    {#if possible}
      <p id="{id}-body">{m.mandates_new_body()}</p>
      <SelectField label={m.mandates_new_agent()} bind:value={agent} options={agentOptions} />
      <SelectField label={m.mandates_new_template()} bind:value={chosen} options={templateOptions} onchange={pickTemplate} />
      {#if picked}
        <div class="picked" role="group" aria-label={m.template_what({ template: templateTitle(picked) })}>
          {#if templateDescription(picked)}<p>{templateDescription(picked)}</p>{/if}
          <PlainWords draft={picked.draft} {catalog} locale={getLocale()} />
        </div>
      {/if}
      <TextField label={m.editor_name()} bind:value={name} bind:element={nameField} error={nameError} maxlength={NAME_MAX} oninput={() => (named = true)} />
    {:else}
      <p id="{id}-body">{m.mandates_new_none()}</p>
      <a href={href({ name: 'agents', add: true })} onclick={onclose}>{m.mandates_agents_link()}</a>
    {/if}
    {#if confirming}
      <CriticalTemplateConfirm
        template={titleOf(chosen, templates)}
        agent={agentName}
        rules={confirming}
        {catalog}
        locale={getLocale()}
        {busy}
        oncancel={() => (confirming = null)}
        onconfirm={() => void send(true)}
      />
    {/if}
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" onclick={onclose}>{m.common_cancel()}</Button>
      {#if possible}<Button type="submit" variant="primary" size="lg" {busy}>{m.mandates_new_create()}</Button>{/if}
    </div>
  </form>
</Dialog>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
  }
  h2 {
    margin: 0;
    font-size: var(--hm-font-size-xl);
    font-weight: var(--hm-font-weight-semibold);
  }
  p {
    margin: 0;
    font-size: 15px;
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  a {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    min-block-size: var(--hm-size-touch);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  .picked {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
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
    justify-content: flex-end;
    gap: var(--hm-space-3);
  }
</style>
