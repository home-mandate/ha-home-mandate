<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  New mandate: a copy of a template for an active agent that has no active mandate (a
  mandate always belongs to one agent, decision D3). The server checks again; a conflict
  means someone else was faster.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { Agent, Template } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { NAME_MAX } from '../../mandate/problems.ts';
  import { templateName } from '../../mandate/template.ts';
  import { href } from '../../router.ts';
  import { clientIdentity } from '../../ui/identity.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import TextField from '../TextField.svelte';

  interface Props {
    open: boolean;
    api: ApiClient;
    /** Active agents without an active mandate. */
    agents: readonly Agent[];
    templates: readonly Template[];
    /** Template chosen on the list; the first one otherwise. */
    template: string | null;
    onclose: () => void;
    oncreated: (id: string) => void;
  }

  let { open, api, agents, templates, template, onclose, oncreated }: Props = $props();

  const id = $props.id();
  let agent = $state('');
  let chosen = $state('');
  let name = $state('');
  let named = $state(false);
  let checked = $state(false);
  let busy = $state(false);
  let error = $state('');
  let nameField: HTMLInputElement | undefined = $state();

  $effect(() => {
    if (!open) return;
    untrack(() => {
      agent = agents[0]?.client_id ?? '';
      chosen = template ?? templates[0]?.name ?? '';
      name = templateName(chosen);
      named = false;
      checked = false;
      busy = false;
      error = '';
    });
  });

  const agentOptions = $derived(
    agents.map((a) => {
      const identity = clientIdentity(a.oauth_client);
      const label = cleanUntrusted(a.display_name);
      return { value: a.client_id, label: identity ? `${label} · ${identity.text}` : label };
    }),
  );
  const templateOptions = $derived(templates.map((t) => ({ value: t.name, label: templateName(t.name) })));
  const length = $derived([...name.trim()].length);
  const nameError = $derived(checked && (length < 1 || length > NAME_MAX) ? m.validation_name({ max: NAME_MAX }) : '');
  const possible = $derived(agents.length > 0 && templates.length > 0);

  function pickTemplate(next: string) {
    if (!named) name = templateName(next);
  }

  async function create(event: SubmitEvent) {
    event.preventDefault();
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
      const created = await api.createMandate({ client_id: agent, template: chosen, name: name.trim() });
      oncreated(created.summary.id);
    } catch (err) {
      error = err instanceof ApiError && err.code === 'conflict' ? m.mandates_new_conflict() : m.mandates_new_failed();
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
      <TextField label={m.editor_name()} bind:value={name} bind:element={nameField} error={nameError} maxlength={NAME_MAX} oninput={() => (named = true)} />
    {:else}
      <p id="{id}-body">{m.mandates_new_none()}</p>
      <a href={href({ name: 'agents' })} onclick={onclose}>{m.mandates_agents_link()}</a>
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
