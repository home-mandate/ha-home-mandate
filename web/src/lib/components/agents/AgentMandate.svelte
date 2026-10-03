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
  import type { Agent, Template } from '../../api/types.ts';
  import { formatNumber, type FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { templateName } from '../../mandate/template.ts';
  import { currentNumber } from '../../mandate/versions.ts';
  import { href } from '../../router.ts';
  import { toasts } from '../../ui/toasts.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import MandateStatus from '../mandate/MandateStatus.svelte';

  interface Props {
    api: ApiClient;
    agent: Agent;
    /** Names of the templates; empty when they could not be loaded. */
    templates: readonly Pick<Template, 'name'>[];
    ctx: FormatContext;
    headingId: string;
  }

  let { api, agent, templates, ctx, headingId }: Props = $props();

  let template = $state('');
  let busy = $state(false);
  let error = $state('');

  const mandate = $derived(agent.mandate);
  const active = $derived(agent.status === 'active');
  const replaces = $derived(mandate?.status === 'active');
  const options = $derived(templates.map((t) => ({ value: t.name, label: templateName(t.name) })));
  const chosen = $derived(template || (templates[0]?.name ?? ''));
  const rate = $derived(
    mandate?.max_actions_per_hour == null
      ? m.agent_detail_rate_none()
      : m.agent_detail_rate_usage({ used: formatNumber(agent.actions_last_hour, ctx), limit: formatNumber(mandate.max_actions_per_hour, ctx) }),
  );

  async function change() {
    if (busy || chosen === '') return;
    busy = true;
    error = '';
    try {
      const detail = mandate && replaces
        ? await api.applyTemplate(mandate.id, { template: chosen, base_digest: (await api.mandate(mandate.id)).summary.digest })
        : await api.createMandate({ client_id: agent.client_id, template: chosen, name: templateName(chosen) });
      toasts.show({ kind: 'success', text: m.toast_saved({ version: currentNumber(detail.versions) }) });
    } catch (err) {
      // A conflict on creating means another mandate came first; on changing, retrying reads the new version.
      error = err instanceof ApiError && err.code === 'conflict' && !replaces ? m.mandates_new_conflict() : m.agent_detail_change_failed();
    } finally {
      busy = false;
    }
  }
</script>

<section aria-labelledby={headingId}>
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
        onchange={(v) => (template = v)}
      />
      <Button {busy} onclick={change}>{m.agent_detail_change_apply()}</Button>
    </div>
    <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
  {/if}
</section>

<style>
  section {
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
