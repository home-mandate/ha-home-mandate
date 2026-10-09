<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  After a template was saved and mandates took their rules from it (#18): the human
  decides what the change affects. "Only the template" closes; otherwise the chosen
  mandates take the change over, each as a new version on its own (name and validity
  stay). Mandates edited since are not chosen at first and are marked: taking over
  replaces their edits. One separate confirmation covers every chosen mandate when the
  template allows critical actions without approval, naming their agents; without it
  nothing changes. Afterwards the result per mandate is shown. A refused one is never sent
  again as it is: "Load again" reloads the list, shows the mandate as it is now, unchosen,
  and only a new choice sends it, with the version just loaded (nobody overwrites a
  version they have not seen). Nothing happens in the background.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { DeviceCatalog, Template, TemplateUser } from '../../api/types.ts';
  import { formatDateTime, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { grantsCritical, merged, preselected, refusedCount, resultText, retryable, targetsOf, type Results } from '../../mandate/rollout.ts';
  import { templateTitle } from '../../mandate/template.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';
  import CriticalTemplateConfirm from './CriticalTemplateConfirm.svelte';

  interface Props {
    open: boolean;
    api: Pick<ApiClient, 'applyTemplateToMandates' | 'templateUsage'>;
    /** The template as saved: its digest is what the mandates take over. */
    template: Template;
    /** The mandates whose rules came from it (GET api/templates/{name}/usage). */
    users: readonly TemplateUser[];
    catalog: DeviceCatalog;
    ctx: FormatContext;
    onclose: () => void;
  }

  let { open, api, template, users, catalog, ctx, onclose }: Props = $props();

  const id = $props.id();
  const chosen = new SvelteSet<string>();
  /** The mandates as last loaded; a mandate loaded again replaces its entry. */
  let list = $state.raw<readonly TemplateUser[]>([]);
  /** Mandates loaded again after a refusal: shown as they are now, to be chosen anew. */
  const reloaded = new SvelteSet<string>();
  /** Refused mandates that no longer use the template: nothing to load again. */
  const gone = new SvelteSet<string>();
  let results = $state<Results>({});
  let confirming = $state<readonly TemplateUser[] | null>(null);
  /** The human confirmed critical actions without approval for the chosen mandates. */
  let confirmed = $state(false);
  let busy = $state(false);
  let error = $state('');
  let applyButton: HTMLButtonElement | undefined = $state();

  $effect(() => {
    if (!open) return;
    untrack(() => {
      chosen.clear();
      for (const mandateId of preselected(users)) chosen.add(mandateId);
      list = users;
      reloaded.clear();
      gone.clear();
      results = {};
      confirming = null;
      confirmed = false;
      busy = false;
      error = '';
    });
  });

  const done = $derived(Object.keys(results).length > 0);
  const shown = $derived(done ? list.filter((u) => results[u.mandate_id] !== undefined || reloaded.has(u.mandate_id)) : list);
  /** The mandates that can be chosen: all at first, afterwards those loaded again. */
  const pending = $derived(shown.filter((u) => results[u.mandate_id] === undefined));
  const chosenCount = $derived(pending.filter((u) => chosen.has(u.mandate_id)).length);
  const criticalRules = $derived(template.draft.rules.filter((r) => r.allow_critical === true));
  const title = $derived(templateTitle(template));
  const updated = $derived(Object.values(results).filter((r) => r === 'updated' || r === 'unchanged').length);

  function toggle(mandateId: string, on: boolean) {
    if (on) chosen.add(mandateId);
    else chosen.delete(mandateId);
  }

  function all(on: boolean) {
    for (const u of pending) toggle(u.mandate_id, on && !u.up_to_date);
  }

  /** apply takes the change over into the given mandates; the separate confirmation first when needed. */
  async function apply(targets: readonly TemplateUser[]) {
    if (busy || targets.length === 0) return;
    if (!confirmed && grantsCritical(template.draft)) {
      confirming = targets;
      return;
    }
    busy = true;
    error = '';
    try {
      const answer = await api.applyTemplateToMandates(template.name, {
        template_digest: template.digest,
        targets: targetsOf(targets, new Set(targets.map((u) => u.mandate_id))),
        ...(confirmed ? { confirm_critical: true } : {}),
      });
      results = merged(results, answer.results);
      for (const u of targets) reloaded.delete(u.mandate_id);
      confirming = null;
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'internal';
      if (code === 'critical_confirmation_required' && !confirmed) confirming = targets;
      else error = code === 'conflict' ? m.rollout_template_changed() : m.rollout_failed();
    } finally {
      busy = false;
    }
  }

  function confirm() {
    const list = confirming;
    confirmed = true;
    if (list) void apply(list);
  }

  /**
   * reload loads the list again for a refused mandate and shows it as it is now, unchosen:
   * only a new choice sends it, with the version just loaded. Never the version the
   * server named in its refusal, which nobody here has seen. A rule allowing critical
   * actions without approval is confirmed anew.
   */
  async function reload(u: TemplateUser) {
    if (busy) return;
    busy = true;
    error = '';
    try {
      const usage = await api.templateUsage(template.name);
      if (usage.digest !== template.digest) {
        error = m.rollout_template_changed();
        return;
      }
      const fresh = usage.mandates.find((x) => x.mandate_id === u.mandate_id);
      if (!fresh) {
        gone.add(u.mandate_id); // revoked or no longer from this template: its result stays
        return;
      }
      list = list.map((x) => (x.mandate_id === fresh.mandate_id ? fresh : x));
      const { [fresh.mandate_id]: _old, ...rest } = results;
      void _old;
      results = rest;
      chosen.delete(fresh.mandate_id);
      reloaded.add(fresh.mandate_id);
      confirmed = false;
    } catch {
      error = m.rollout_reload_failed();
    } finally {
      busy = false;
    }
    await tick();
    document.getElementById(`${id}-${u.mandate_id}`)?.focus();
  }

  function cancelCritical() {
    confirming = null;
    applyButton?.focus();
  }

  const chosenUsers = () => pending.filter((u) => chosen.has(u.mandate_id));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" size="lg" {onclose}>
  <div class="content">
    <h2 id="{id}-title">{m.rollout_title()}</h2>
    <p id="{id}-body" class="body">{m.rollout_body()}</p>

    {#if done}
      <p class="status" role="status">{m.rollout_status({ updated, refused: refusedCount(results) })}</p>
    {/if}
    {#if pending.length > 0}
      <div class="bulk">
        <Button size="md" disabled={busy} onclick={() => all(true)}>{m.rollout_select_all()}</Button>
        <Button size="md" disabled={busy} onclick={() => all(false)}>{m.rollout_select_none()}</Button>
      </div>
    {/if}

    <ul role="list" aria-label={m.rollout_list_label()}>
      {#each shown as u (u.mandate_id)}
        {@const result = results[u.mandate_id]}
        <li class:edited={u.edited_since}>
          {#if !result}
            <input
              id="{id}-{u.mandate_id}"
              type="checkbox"
              checked={chosen.has(u.mandate_id)}
              disabled={busy || u.up_to_date}
              aria-describedby="{id}-{u.mandate_id}-about"
              onchange={(e) => toggle(u.mandate_id, e.currentTarget.checked)}
            />
          {/if}
          <div class="text">
            {#if result}
              <span class="agent"><bdi>{cleanUntrusted(u.agent_display_name)}</bdi></span>
            {:else}
              <label for="{id}-{u.mandate_id}" class="agent"><bdi>{cleanUntrusted(u.agent_display_name)}</bdi></label>
            {/if}
            <span id="{id}-{u.mandate_id}-about" class="about">
              <span>{m.rollout_mandate({ mandate: isolate(u.mandate_name) })}</span>
              <span>{m.rollout_taken({ date: formatDateTime(new Date(u.taken_at), ctx) })}</span>
              {#if u.up_to_date}<span>{m.rollout_up_to_date()}</span>{/if}
              {#if !result && reloaded.has(u.mandate_id)}<span class="warn"><Icon name="info" size={16} />{m.rollout_reloaded()}</span>{/if}
              {#if u.edited_since}<span class="warn"><Icon name="warning" size={16} />{m.rollout_edited()}</span>{/if}
            </span>
            {#if result}
              <span class="result" class:refused={result !== 'updated' && result !== 'unchanged'}>{resultText(result)}</span>
            {/if}
          </div>
          {#if result && retryable(result) && !gone.has(u.mandate_id)}
            <Button size="md" disabled={busy} onclick={() => void reload(u)} aria-label={m.rollout_reload_for({ agent: isolate(u.agent_display_name) })}>
              {m.rollout_reload()}
            </Button>
          {/if}
        </li>
      {/each}
    </ul>

    {#if confirming}
      <CriticalTemplateConfirm
        template={title}
        agents={confirming.map((u) => u.agent_display_name)}
        rules={criticalRules}
        {catalog}
        locale={ctx.locale}
        {busy}
        oncancel={cancelCritical}
        onconfirm={confirm}
      />
    {/if}

    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
    <div class="actions">
      <Button size="lg" variant={pending.length > 0 ? 'secondary' : 'primary'} disabled={busy} onclick={onclose}>
        {done ? m.common_close() : m.rollout_only_template()}
      </Button>
      {#if pending.length > 0}
        <Button
          bind:element={applyButton}
          size="lg"
          variant="primary"
          {busy}
          disabled={chosenCount === 0 || confirming !== null}
          onclick={() => void apply(chosenUsers())}
        >
          {m.rollout_apply({ count: chosenCount })}
        </Button>
      {/if}
    </div>
  </div>
</Dialog>

<style>
  .content {
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
    text-wrap: pretty;
  }
  .body,
  .status {
    color: var(--hm-color-text-muted);
  }
  .bulk {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
  ul {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    max-block-size: 50vh;
    overflow-y: auto;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-3);
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  li.edited {
    border-color: var(--hm-color-warning-border);
  }
  input[type='checkbox'] {
    flex-shrink: 0;
    margin-block: 3px 0;
    margin-inline: 0;
    inline-size: 18px;
    block-size: 18px;
    accent-color: var(--hm-color-accent);
  }
  input[type='checkbox']:focus-visible {
    outline: var(--hm-focus-width) solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
    flex: 1;
    min-inline-size: 0;
    overflow-wrap: anywhere;
  }
  .agent {
    font-weight: var(--hm-font-weight-semibold);
  }
  .about {
    display: flex;
    flex-direction: column;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .warn {
    display: flex;
    gap: 6px;
    color: var(--hm-color-warning-fg);
  }
  .result {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
    color: var(--hm-color-text);
  }
  .result.refused {
    color: var(--hm-color-danger-fg);
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
