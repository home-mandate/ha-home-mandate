<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  One audit entry on its own page (#/audit/:seq): the mobile view of the details and the
  target of the "broken chain" banner. An entry that is gone (retention) is "not found".
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { Loader } from '../app/loader.svelte.ts';
  import type { AppState } from '../app/state.svelte.ts';
  import type { AuditEntry, DeviceCatalog } from '../api/types.ts';
  import ErrorState from '../components/ErrorState.svelte';
  import FullPageState from '../components/FullPageState.svelte';
  import Icon from '../components/Icon.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import AuditDetail from '../components/audit/AuditDetail.svelte';
  import { m } from '../i18n.ts';
  import { getLocale } from '../paraglide/runtime.js';
  import { href } from '../router.ts';

  interface Props {
    app: AppState;
    seq: number;
  }

  interface Data {
    entry: AuditEntry | null;
    catalog: DeviceCatalog | null;
  }

  let { app, seq }: Props = $props();

  const id = $props.id();
  const SKELETON = ['40%', '70%', '60%'];

  const loader = new Loader<Data>(async () => {
    const [page, catalog] = await Promise.all([app.api.audit({ before: seq + 1, limit: 1 }), app.api.devices().catch(() => null)]);
    const entry = page.entries[0];
    return { entry: entry?.seq === seq ? entry : null, catalog };
  });

  onMount(() => void loader.run());

  const ctx = $derived({ locale: getLocale(), timeZone: app.session?.household.time_zone ?? 'UTC' });
  const chain = $derived(app.system?.chain ?? null);
</script>

<a class="back" href={href({ name: 'audit', query: {} })}><Icon name="back" size={16} />{m.audit_title()}</a>

{#if loader.status === 'error'}
  <ErrorState title={m.audit_error_title()} body={m.audit_error_body()} onretry={() => void loader.run()} />
{:else if !loader.data}
  <Skeleton lines={SKELETON} />
{:else if !loader.data.entry}
  <FullPageState icon="search" badge="info" title={m.notfound_title()} body={m.notfound_body()} />
{:else}
  {@const entry = loader.data.entry}
  <article aria-labelledby="{id}-title">
    <AuditDetail
      {entry}
      catalog={loader.data.catalog}
      {ctx}
      broken={chain !== null && !chain.valid && chain.broken_at_seq !== null && entry.seq >= chain.broken_at_seq}
      headingId="{id}-title"
      level={1}
    />
  </article>
{/if}

<style>
  .back {
    align-self: flex-start;
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
    min-block-size: var(--hm-size-touch);
    color: var(--hm-color-accent-text);
    font-weight: var(--hm-font-weight-medium);
  }
  article {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    padding: var(--hm-space-4);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
  }
</style>
