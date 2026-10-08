<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  The agent's mandate on its detail page (design README 6.2; decisions D3, G7; #16): name
  with status, where its rules came from, the rate limit and how much of it the last hour
  used, and taking over rules from a template. An active mandate takes the template as a
  new version (rules only; validity stays, and so does the name unless the human takes
  the proposed one while the mandate is still named after its previous template); an
  active agent without an active mandate gets a new one, named after the agent. A revoked
  agent cannot get one: revoking is final.
-->
<script lang="ts">
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { Agent, DeviceCatalog, Rule, Template } from '../../api/types.ts';
  import { formatNumber, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { renameProposal, rulesFromText } from '../../mandate/origin.ts';
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
  import RenameChoice from './RenameChoice.svelte';

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
  /** The human takes the proposed name (the default while one is proposed). */
  let rename = $state(true);

  const mandate = $derived(agent.mandate);
  const active = $derived(agent.status === 'active');
  const replaces = $derived(mandate?.status === 'active');
  const options = $derived(templates.map((t) => ({ value: t.name, label: templateTitle(t) })));
  // A template that is no longer offered falls back to the first one.
  const chosen = $derived(templates.some((t) => t.name === template) ? template : (templates[0]?.name ?? ''));
  const picked = $derived(templates.find((t) => t.name === chosen) ?? null);
  const origin = $derived(mandate ? rulesFromText(mandate.rules_from, templates, ctx) : '');
  /** The agent's name, proposed while the mandate is still named after the template its rules came from. */
  const proposal = $derived(mandate && replaces ? renameProposal(mandate.name, agent.display_name, mandate.rules_from, templates) : null);
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
      const renamed = proposal !== null && rename ? { name: proposal } : {};
      // The version shown here is the base: a change by someone else since then is a conflict.
      // A new mandate is named after the agent by the server.
      const detail = mandate && replaces
        ? await api.applyTemplate(mandate.id, { template: chosen, base_digest: mandate.digest, ...renamed, ...extra })
        : await api.createMandate({ client_id: agent.client_id, template: chosen, ...extra });
      confirming = null;
      const unchanged = 'result' in detail && detail.result === 'unchanged';
      toasts.show({ kind: 'success', text: unchanged ? m.agent_detail_change_unchanged() : m.toast_saved({ version: currentNumber(detail.versions) }) });
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
      {#if mandate.status === 'active'}<a class="rename" href={href({ name: 'mandate', id: mandate.id })}>{m.agent_detail_rename()}</a>{/if}
    {:else}
      <span class="muted">{m.agents_no_mandate()}</span>
    {/if}
  </div>
  {#if origin}<p class="muted">{origin}</p>{/if}
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
      {#if proposal !== null && mandate}
        <RenameChoice current={mandate.name} proposed={proposal} bind:rename />
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
  .current a.rename {
    margin-inline-start: auto;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
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
