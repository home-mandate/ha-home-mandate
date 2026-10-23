<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Devices that left or entered an area that rules name (SPEC-v0 section 3.4): a rule by
  area applies to the devices that are in the area now, so it stopped or started applying
  to them. The household sees which mandates that affects; "Seen" ends the notice. It
  sends the areas that were shown, so that a later move is never dismissed unseen.
-->
<script lang="ts">
  import { ApiError, type ApiClient } from '../../api/client.ts';
  import type { AreaMove, DeviceCatalog } from '../../api/types.ts';
  import { m } from '../../i18n.ts';
  import { areaLabel } from '../../mandate/labels.ts';
  import { href } from '../../router.ts';
  import { cleanUntrusted, isolate } from '../../untrusted.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  interface Props {
    moves: AreaMove[];
    catalog: DeviceCatalog;
    api: ApiClient;
    locale: string;
    /** After every attempt, done or not: the list reloads; text says what was done. */
    onresolved: (text: string | null) => void;
  }

  let { moves, catalog, api, locale, onresolved }: Props = $props();

  const id = $props.id();
  let busy = $state<string | null>(null);
  let error = $state('');

  const nameOf = (mv: AreaMove) => cleanUntrusted(mv.name) || cleanUntrusted(mv.entity_id);
  function areasText(areas: readonly string[]): string {
    if (areas.length === 0) return m.moves_none();
    const names = areas.map((a) => {
      const known = catalog.areas.find((x) => x.id === a);
      return isolate(known ? areaLabel(known) : cleanUntrusted(a));
    });
    return new Intl.ListFormat(locale, { type: 'conjunction' }).format(names);
  }

  async function dismiss(mv: AreaMove) {
    error = '';
    busy = mv.entity_id;
    try {
      await api.dismissAreaMove(mv.entity_id, mv.current);
      onresolved(m.moves_dismissed({ device: isolate(nameOf(mv)) }));
    } catch (err) {
      error = err instanceof ApiError && err.code === 'conflict' ? m.moves_changed() : m.moves_failed();
      onresolved(null); // moved again or gone: show what is true now
    } finally {
      busy = null;
    }
  }
</script>

<section class="moves" aria-labelledby="{id}-title">
  <h2 id="{id}-title"><Icon name="warning" />{m.moves_title()}</h2>
  <p class="intro">{m.moves_intro()}</p>
  <ul role="list">
    {#each moves as mv, i (mv.entity_id)}
      <li>
        <p class="what">
          <strong><bdi>{nameOf(mv)}</bdi></strong>
          <span>{m.moves_from_to({ previous: areasText(mv.previous), current: areasText(mv.current) })}</span>
        </p>
        <div class="where">
          <span id="{id}-{i}-where">{m.moves_mandates({ count: mv.mandates.length })}</span>
          <ul role="list" aria-labelledby="{id}-{i}-where">
            {#each mv.mandates as md (md.id)}
              <li><a href={href({ name: 'mandate', id: md.id })}><bdi>{cleanUntrusted(md.name) || cleanUntrusted(md.id)}</bdi></a></li>
            {/each}
          </ul>
        </div>
        <div class="actions">
          <Button busy={busy === mv.entity_id} onclick={() => void dismiss(mv)}>{m.moves_dismiss()}</Button>
        </div>
      </li>
    {/each}
  </ul>
  <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
</section>

<style>
  .moves {
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
  ul {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .where ul {
    flex-direction: row;
    flex-wrap: wrap;
    gap: var(--hm-space-1) var(--hm-space-3);
  }
  .intro,
  .where {
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
  .moves > ul > li {
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
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-2);
  }
  .error:empty {
    display: none;
  }
  .error {
    display: flex;
    align-items: center;
    gap: var(--hm-space-1);
    color: var(--hm-color-danger-fg);
    font-size: var(--hm-font-size-sm);
  }
</style>
