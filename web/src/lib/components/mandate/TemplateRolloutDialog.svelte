<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  After a template was saved and mandates took their rules from it (#18): the human
  decides what the change affects. "Only the template" closes; otherwise the chosen
  mandates take the change over, each as a new version on its own (name and validity
  stay). Mandates edited since are not chosen at first and are marked: taking over
  replaces their edits. One separate confirmation covers every chosen mandate when the
  template allows critical actions without approval, naming their agents; without it
  nothing changes. Afterwards the result per mandate is shown; refused ones can be tried
  again one by one. Nothing happens in the background.
-->
<script lang="ts">
  import { untrack } from 'svelte';
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
    api: Pick<ApiClient, 'applyTemplateToMandates'>;
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
  /** The latest version of each mandate the server named; a retry starts from it. */
  let digests = $state<Record<string, string>>({});
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
      digests = {};
      results = {};
      confirming = null;
      confirmed = false;
      busy = false;
      error = '';
    });
  });

  const done = $derived(Object.keys(results).length > 0);
  const current = $derived(users.map((u) => ({ ...u, digest: digests[u.mandate_id] ?? u.digest })));
  const shown = $derived(done ? current.filter((u) => results[u.mandate_id] !== undefined) : current);
  const criticalRules = $derived(template.draft.rules.filter((r) => r.allow_critical === true));
  const title = $derived(templateTitle(template));
  const updated = $derived(Object.values(results).filter((r) => r === 'updated' || r === 'unchanged').length);

  function toggle(mandateId: string, on: boolean) {
    if (on) chosen.add(mandateId);
    else chosen.delete(mandateId);
  }

  function all(on: boolean) {
    for (const u of users) toggle(u.mandate_id, on && !u.up_to_date);
  }

  /** apply takes the change over into the given mandates; the separate confirmation first when needed. */
  async function apply(list: readonly TemplateUser[]) {
    if (busy || list.length === 0) return;
    if (!confirmed && grantsCritical(template.draft)) {
      confirming = list;
      return;
    }
    busy = true;
    error = '';
    try {
      const answer = await api.applyTemplateToMandates(template.name, {
        template_digest: template.digest,
        targets: targetsOf(list, new Set(list.map((u) => u.mandate_id))),
        ...(confirmed ? { confirm_critical: true } : {}),
      });
      results = merged(results, answer.results);
      digests = { ...digests, ...Object.fromEntries(answer.results.flatMap((r) => (r.digest === null ? [] : [[r.mandate_id, r.digest]]))) };
      confirming = null;
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'internal';
      if (code === 'critical_confirmation_required' && !confirmed) confirming = list;
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
   * retry tries one refused mandate again, from the version the server named. A rule
   * allowing critical actions without approval is confirmed anew: the confirmation was
   * for the versions the human saw.
   */
  function retry(u: TemplateUser) {
    confirmed = false;
    void apply([u]);
  }

  function cancelCritical() {
    confirming = null;
    applyButton?.focus();
  }

  const chosenUsers = () => current.filter((u) => chosen.has(u.mandate_id));
</script>

<Dialog {open} labelledby="{id}-title" describedby="{id}-body" size="lg" {onclose}>
  <div class="content">
    <h2 id="{id}-title">{m.rollout_title()}</h2>
    <p id="{id}-body" class="body">{m.rollout_body()}</p>

    {#if !done}
      <div class="bulk">
        <Button size="md" disabled={busy} onclick={() => all(true)}>{m.rollout_select_all()}</Button>
        <Button size="md" disabled={busy} onclick={() => all(false)}>{m.rollout_select_none()}</Button>
      </div>
    {:else}
      <p class="status" role="status">{m.rollout_status({ updated, refused: refusedCount(results) })}</p>
    {/if}

    <ul role="list" aria-label={m.rollout_list_label()}>
      {#each shown as u (u.mandate_id)}
        {@const result = results[u.mandate_id]}
        <li class:edited={u.edited_since}>
          {#if !done}
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
            {#if done}
              <span class="agent"><bdi>{cleanUntrusted(u.agent_display_name)}</bdi></span>
            {:else}
              <label for="{id}-{u.mandate_id}" class="agent"><bdi>{cleanUntrusted(u.agent_display_name)}</bdi></label>
            {/if}
            <span id="{id}-{u.mandate_id}-about" class="about">
              <span>{m.rollout_mandate({ mandate: isolate(u.mandate_name) })}</span>
              <span>{m.rollout_taken({ date: formatDateTime(new Date(u.taken_at), ctx) })}</span>
              {#if u.up_to_date}<span>{m.rollout_up_to_date()}</span>{/if}
              {#if u.edited_since}<span class="warn"><Icon name="warning" size={16} />{m.rollout_edited()}</span>{/if}
            </span>
            {#if result}
              <span class="result" class:refused={result !== 'updated' && result !== 'unchanged'}>{resultText(result)}</span>
            {/if}
          </div>
          {#if result && retryable(result)}
            <Button size="md" disabled={busy} onclick={() => retry(u)} aria-label={m.rollout_retry({ agent: isolate(u.agent_display_name) })}>
              {m.common_retry()}
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
      {#if done}
        <Button size="lg" variant="primary" disabled={busy} onclick={onclose}>{m.common_close()}</Button>
      {:else}
        <Button size="lg" disabled={busy} onclick={onclose}>{m.rollout_only_template()}</Button>
        <Button
          bind:element={applyButton}
          size="lg"
          variant="primary"
          {busy}
          disabled={chosen.size === 0 || confirming !== null}
          onclick={() => void apply(chosenUsers())}
        >
          {m.rollout_apply({ count: chosen.size })}
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
