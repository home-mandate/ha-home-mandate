<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Devices Home Assistant renamed while mandates still name them by a former ID. Until a
  human decides, those rules keep applying to the renamed device and the stricter
  decision wins. "Take over" stores new versions with the current ID; a rule that allows
  critical actions without approval needs the separate confirmation first. "Don't take
  over" lets the rules go, which can lower the protection, so it asks inline first.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { Rename } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { href } from '../../router.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    renames: Rename[];
    api: ApiClient;
    locale: string;
  }

  let { renames, api, locale }: Props = $props();

  const id = $props.id();
  const DISMISS = 'dismiss';
  const CRITICAL = 'critical';
  type Kind = typeof DISMISS | typeof CRITICAL;
  /** The rename whose confirmation is open, and which one. */
  let pending = $state<{ entity: string; kind: Kind } | null>(null);
  let busy = $state<string | null>(null);
  let error = $state('');
  let confirmCancel: HTMLButtonElement | undefined = $state();

  const list = (items: readonly string[]) => new Intl.ListFormat(locale, { type: 'conjunction' }).format(items);
  const formersOf = (r: Rename) => list(r.formers.map((f) => isolate(cleanUntrusted(f))));
  const nameOf = (r: Rename) => cleanUntrusted(r.name) || cleanUntrusted(r.entity_id);

  // A rename resolved elsewhere while its confirmation is open: nothing left to confirm.
  $effect(() => {
    const open = pending;
    if (open && !renames.some((r) => r.entity_id === open.entity)) pending = null;
  });

  async function ask(entity: string, kind: Kind) {
    pending = { entity, kind };
    await tick();
    confirmCancel?.focus();
  }

  async function run(entity: string, action: () => Promise<void>) {
    error = '';
    busy = entity;
    try {
      await action();
      pending = null;
    } catch (err) {
      if (err instanceof ApiError && err.code === 'critical_confirmation_required') await ask(entity, CRITICAL);
      else error = m.renames_failed();
    } finally {
      busy = null;
    }
  }

  function apply(r: Rename) {
    if (r.mandates.some((md) => md.critical)) void ask(r.entity_id, CRITICAL);
    else void run(r.entity_id, () => api.applyRename(r.entity_id));
  }
</script>

<section class="renames" aria-labelledby="{id}-title">
  <h2 id="{id}-title"><Icon name="warning" />{m.renames_title()}</h2>
  <p class="intro">{m.renames_intro()}</p>
  <ul role="list">
    {#each renames as r (r.entity_id)}
      <li>
        <p class="what">
          <strong><bdi>{nameOf(r)}</bdi></strong>
          <span>{m.renames_was({ formers: formersOf(r), entity: isolate(cleanUntrusted(r.entity_id)) })}</span>
        </p>
        <p class="where">
          {m.renames_mandates({ count: r.mandates.length })}
          {#each r.mandates as md, i (md.id)}{#if i > 0},{/if}
            <a href={href({ name: 'mandate', id: md.id })}><bdi>{cleanUntrusted(md.name) || md.id}</bdi></a>{/each}
        </p>
        {#if pending?.entity === r.entity_id}
          <div class="confirm" role="group" aria-labelledby="{id}-{r.entity_id}-confirm">
            <p id="{id}-{r.entity_id}-confirm">
              {pending.kind === DISMISS ? m.renames_dismiss_text({ formers: formersOf(r) }) : m.renames_critical_text()}
            </p>
            <div class="actions">
              <Button bind:element={confirmCancel} onclick={() => (pending = null)}>{m.common_cancel()}</Button>
              {#if pending.kind === DISMISS}
                <Button variant="danger" busy={busy === r.entity_id} onclick={() => void run(r.entity_id, () => api.dismissRename(r.entity_id))}>
                  {m.renames_dismiss()}
                </Button>
              {:else}
                <Button variant="danger" busy={busy === r.entity_id} onclick={() => void run(r.entity_id, () => api.applyRename(r.entity_id, true))}>
                  {m.renames_apply_critical()}
                </Button>
              {/if}
            </div>
          </div>
        {:else}
          <div class="actions">
            <Button variant="primary" busy={busy === r.entity_id} onclick={() => apply(r)}>{m.renames_apply()}</Button>
            <Button variant="text" onclick={() => void ask(r.entity_id, DISMISS)}>{m.renames_dismiss()}</Button>
          </div>
        {/if}
      </li>
    {/each}
  </ul>
  <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
</section>

<style>
  .renames {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-4);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-warning-bg);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
  }
  h2 {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    margin: 0;
    font-size: var(--hm-font-size-lg);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-warning-fg);
  }
  p {
    margin: 0;
  }
  .intro,
  .where {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  ul {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
    padding-block-start: var(--hm-space-3);
    border-block-start: var(--hm-border-width) solid var(--hm-color-warning-border);
    min-inline-size: 0;
    overflow-wrap: anywhere;
  }
  .what {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-1) var(--hm-space-2);
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-warning-border);
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-3);
  }
  .error {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
    color: var(--hm-color-danger-fg);
  }
  .error:empty {
    display: none;
  }
</style>
