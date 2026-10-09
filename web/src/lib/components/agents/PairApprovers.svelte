<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Who may approve after the admission with the chosen template (issue: show who may
  approve, warn when nobody is reachable): the people the approvers placeholder stands for
  and those the template names, each with how requests reach them, and a warning when
  nobody can answer ordinary or critical requests. It never blocks the approval; the human
  decides. When the server cannot tell (Home Assistant not reachable, or the request
  failed), it says so and never shows anyone as reachable. Names come from Home Assistant:
  untrusted text, shown as text only.
-->
<script lang="ts">
  import type { TemplateApprover, TemplateApprovers } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { href } from '../../router.ts';
  import { cleanUntrusted } from '../../untrusted.ts';
  import Banner from '../Banner.svelte';

  interface Props {
    template: string;
    load: (template: string) => Promise<TemplateApprovers>;
  }

  let { template, load }: Props = $props();

  /** Longest name shown. */
  const NAME_MAX = 80;

  const id = $props.id();
  let data = $state.raw<TemplateApprovers | null>(null);
  let failed = $state(false);

  $effect(() => {
    const name = template;
    let current = true;
    data = null;
    failed = false;
    load(name).then(
      (d) => {
        if (current) data = d;
      },
      () => {
        if (current) failed = true;
      },
    );
    return () => {
      current = false;
    };
  });

  function notes(p: TemplateApprover): string[] {
    if (p.service) return [m.pair_approver_service()];
    if (p.normal === 'unknown' || p.critical === 'unknown') return [m.pair_approver_unknown()];
    if (p.normal === 'none' && p.critical === 'none') return [m.pair_approver_none()];
    const out: string[] = [];
    if (p.normal === 'ui') out.push(m.pair_approver_ui());
    if (p.critical === 'none') out.push(m.pair_approver_no_critical());
    return out;
  }

  const unknown = $derived(failed || data?.normal === 'unknown' || data?.critical === 'unknown');
  /** The human who admits has no channel yet: point to the settings. */
  const selfUnreachable = $derived(data?.people.some((p) => p.self && p.normal === 'none' && p.critical === 'none') ?? false);
</script>

<section class="approvers" aria-labelledby="{id}-title">
  <h3 id="{id}-title">{m.pair_approvers_title()}</h3>
  {#if data}
    {#if data.people.length > 0}
      <p class="hint">{m.pair_approvers_hint()}</p>
      <ul>
        {#each data.people as p (p.user_id)}
          <li>
            <bdi class="name">{p.name === null ? m.pair_approver_unnamed() : cleanUntrusted(p.name, NAME_MAX)}</bdi>
            {#if p.self}<span class="self">{m.pair_approver_self()}</span>{/if}
            {#each notes(p) as note (note)}<span class="note" class:off={p.normal === 'none' || p.service}>{note}</span>{/each}
          </li>
        {/each}
      </ul>
    {/if}
    {#if data.normal === 'nobody'}<Banner kind="warning" body={m.pair_approvers_nobody()} />{/if}
    {#if data.critical === 'nobody'}<Banner kind="warning" body={m.pair_approvers_nobody_critical()} />{/if}
  {:else if !failed}
    <p class="hint">{m.common_loading()}</p>
  {/if}
  {#if unknown}<Banner kind="warning" body={m.pair_approvers_unknown()} />{/if}
  {#if selfUnreachable}<a class="setup" href={href({ name: 'settings', section: 'approvers' })}>{m.pair_approvers_setup()}</a>{/if}
</section>

<style>
  .approvers {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .hint {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
    text-wrap: pretty;
  }
  ul {
    margin: 0;
    padding-inline-start: var(--hm-space-5);
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-1);
  }
  li {
    overflow-wrap: anywhere;
  }
  .name {
    font-weight: var(--hm-font-weight-medium);
  }
  .self,
  .note {
    margin-inline-start: var(--hm-space-2);
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .note.off {
    color: var(--hm-color-warning-fg);
  }
  .setup {
    align-self: flex-start;
    font-size: var(--hm-font-size-sm);
  }
</style>
