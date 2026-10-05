<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The agent's mandate on its detail page (design README 6.2; decisions D3, G7): name with
  status, the rate limit and how much of it the last hour used, and a change from a
  template. An active mandate takes the template as a new version (rules only; name and
  validity stay); an active agent without an active mandate gets a new one. A revoked
  agent cannot get one: revoking is final.
-->
<script lang="ts">
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { Agent, DeviceCatalog, Rule, Template } from '../../api/types.ts';
  import { formatNumber, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { templateDescription, templateTitle, titleOf } from '../../mandate/template.ts';
  import { currentNumber } from '../../mandate/versions.ts';
  import { href } from '../../router.ts';
  import { toasts } from '../../ui/toasts.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import CriticalTemplateConfirm from '../mandate/CriticalTemplateConfirm.svelte';
  import MandateStatus from '../mandate/MandateStatus.svelte';
  import PlainWords from '../mandate/PlainWords.svelte';

  interface Props {
    api: ApiClient;
    agent: Agent;
    /** Templates that are offered (no hidden base templates); empty when they could not be loaded. */
    templates: readonly Template[];
    /** For the plain words of the chosen template; empty without Home Assistant. */
    catalog: DeviceCatalog;
    ctx: FormatContext;
    headingId: string;
  }

  let { api, agent, templates, catalog, ctx, headingId }: Props = $props();

  let template = $state('');
  let busy = $state(false);
  let error = $state('');
  /** The server asked for the separate confirmation (U9): the template and its critical rules. */
  let confirming: { template: string; rules: Rule[] | null } | null = $state(null);
  let apply: HTMLButtonElement | undefined = $state();

  const mandate = $derived(agent.mandate);
  const active = $derived(agent.status === 'active');
  const replaces = $derived(mandate?.status === 'active');
  const options = $derived(templates.map((t) => ({ value: t.name, label: templateTitle(t) })));
  // A template that is no longer offered falls back to the first one.
  const chosen = $derived(templates.some((t) => t.name === template) ? template : (templates[0]?.name ?? ''));
  const picked = $derived(templates.find((t) => t.name === chosen) ?? null);
  const rate = $derived(
    mandate?.max_actions_per_hour == null
      ? m.agent_detail_rate_none()
      : m.agent_detail_rate_usage({ used: formatNumber(agent.actions_last_hour, ctx), limit: formatNumber(mandate.max_actions_per_hour, ctx) }),
  );

  async function change(confirm = false) {
    if (busy || chosen === '') return;
    busy = true;
    error = '';
    const extra = confirm ? { confirm_critical: true } : {};
    try {
      const detail = mandate && replaces
        ? // The version shown here is the base: a change by someone else since then is a conflict.
          await api.applyTemplate(mandate.id, { template: chosen, base_digest: mandate.digest, ...extra })
        : await api.createMandate({ client_id: agent.client_id, template: chosen, name: titleOf(chosen, templates), ...extra });
      confirming = null;
      toasts.show({ kind: 'success', text: m.toast_saved({ version: currentNumber(detail.versions) }) });
    } catch (err) {
      if (!confirm && err instanceof ApiError && err.code === 'critical_confirmation_required') {
        confirming = { template: chosen, rules: await criticalRules(chosen) };
        return;
      }
      confirming = null;
      error = failure(err);
    } finally {
      busy = false;
    }
  }

  function failure(err: unknown): string {
    const code = err instanceof ApiError ? err.code : 'internal';
    // Creating: another mandate came first. Changing: the mandate changed since it was shown.
    if (code === 'conflict') return replaces ? m.agent_detail_change_conflict() : m.mandates_new_conflict();
    if (code === 'no_approvers') return m.template_no_approvers();
    if (code === 'invalid_input' && err instanceof ApiError && err.field === '/template') return m.template_gone();
    return m.agent_detail_change_failed();
  }

  /** The template's rules that allow critical actions without approval; null if it cannot be read. */
  async function criticalRules(name: string): Promise<Rule[] | null> {
    try {
      return (await api.template(name)).draft.rules.filter((r) => r.allow_critical === true);
    } catch {
      return null;
    }
  }

  function cancelCritical() {
    confirming = null;
    apply?.focus();
  }
</script>

<div class="mandate" role="group" aria-labelledby={headingId}>
  <h2 id={headingId}>{m.agent_detail_mandate()}</h2>
  <div class="current">
    {#if mandate}
      <a href={href({ name: 'mandate', id: mandate.id })}><bdi>{cleanUntrusted(mandate.name)}</bdi></a>
      <MandateStatus status={mandate.status} compact />
    {:else}
      <span class="muted">{m.agents_no_mandate()}</span>
    {/if}
  </div>
  {#if mandate}
    <dl>
      <dt>{m.agent_detail_rate()}</dt>
      <dd>{rate}</dd>
    </dl>
  {/if}

  {#if active && options.length > 0}
    <div class="change">
      <SelectField
        label={replaces ? m.agent_detail_change_mandate() : m.pair_mandate_template()}
        value={chosen}
        {options}
        help={replaces ? m.agent_detail_change_help() : undefined}
        onchange={(v) => {
          template = v;
          confirming = null; // another template: its own confirmation
        }}
      />
      {#if picked}
        <div class="picked" role="group" aria-label={m.template_what({ template: templateTitle(picked) })}>
          {#if templateDescription(picked)}<p class="description">{templateDescription(picked)}</p>{/if}
          <PlainWords draft={picked.draft} {catalog} locale={ctx.locale} />
        </div>
      {/if}
      <Button bind:element={apply} {busy} disabled={confirming !== null} onclick={() => change()}>{m.agent_detail_change_apply()}</Button>
    </div>
    {#if confirming}
      <CriticalTemplateConfirm
        template={titleOf(confirming.template, templates)}
        agent={agent.display_name}
        rules={confirming.rules}
        {catalog}
        locale={ctx.locale}
        {busy}
        oncancel={cancelCritical}
        onconfirm={() => change(true)}
      />
    {/if}
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
  {/if}
</div>

<style>
  .mandate {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  h2 {
    margin: 0;
    font-size: 17px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .current {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-2);
    overflow-wrap: anywhere;
  }
  .current a {
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  dl {
    display: grid;
    grid-template-columns: minmax(120px, auto) minmax(0, 1fr);
    gap: var(--hm-space-2) var(--hm-space-4);
    margin: 0;
  }
  dt {
    color: var(--hm-color-text-muted);
  }
  dd {
    margin: 0;
  }
  .change {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-3);
  }
  .change > :global(:first-child) {
    align-self: stretch;
  }
  .picked {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    align-self: stretch;
    padding: var(--hm-space-3) var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface-sunken);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .description {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .muted {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .error {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-danger-fg);
  }
</style>
