<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Summary before a new version is stored (design README 6.5, "Speichern"): the changed
  rules and settings and, computed, what that changes for the agent. A version that allows
  critical actions without approval says so in a separate alert. The safe button comes
  first and gets the focus; Esc keeps editing. Restoring a version goes through the same
  summary.
-->
<script lang="ts">
  import type { DeviceCatalog } from '../../api/types.ts';
  import { validityChange } from '../../engine/analysis.ts';
  import type { FormatContext } from '../../format.ts';
  import { m } from '../../i18n.ts';
  import { ruleChanges, type Edited, type RuleChangeKind } from '../../mandate/changes.ts';
  import { settingLines, type People } from '../../mandate/summary.ts';
  import { ruleText } from '../../mandate/text.ts';
  import Button from '../Button.svelte';
  import Dialog from '../Dialog.svelte';
  import Icon from '../Icon.svelte';
  import EffectsList from './EffectsList.svelte';
  import RuleSentence from './RuleSentence.svelte';

  interface Props {
    open: boolean;
    title: string;
    body: string;
    confirm: string;
    prev: Edited;
    next: Edited;
    catalog: DeviceCatalog;
    people: People;
    ctx: FormatContext;
    /** The new version grants critical actions without approval that the old one did not. */
    critical: boolean;
    /** The device list could not be loaded; the effect cannot be computed. */
    unknown?: boolean;
    busy: boolean;
    error: string;
    onclose: () => void;
    onconfirm: () => void;
  }

  let { open, title, body, confirm, prev, next, catalog, people, ctx, critical, unknown = false, busy, error, onclose, onconfirm }: Props = $props();

  const id = $props.id();
  const TAGS: Record<RuleChangeKind, () => string> = {
    added: () => m.version_rule_added(),
    changed: () => m.version_rule_changed(),
    removed: () => m.version_rule_removed(),
  };

  let back: HTMLButtonElement | undefined = $state();

  const rules = $derived(ruleChanges(prev.draft, next.draft));
  const settings = $derived(settingLines(prev, next, ctx, people));
  const longer = $derived(validityChange(prev.draft, next.draft)?.widening === true);
  const described = $derived(critical ? `${id}-body ${id}-flag` : `${id}-body`);
  // The button says what it does when it is the confirmation for critical actions.
  const confirmLabel = $derived(critical ? `${m.critical_confirm_action()} · ${confirm}` : confirm);
</script>

<Dialog {open} labelledby="{id}-title" describedby={described} destructive={critical} size="lg" initial={back ?? null} {onclose}>
  <div class="intro">
    <h2 id="{id}-title">{title}</h2>
    <p id="{id}-body">{body}</p>
  </div>
  {#if critical}
    <div id="{id}-flag" class="flag"><Icon name="critical" /><span>{m.save_critical_flag()}</span></div>
  {/if}
  {#if longer}
    <div class="flag longer"><Icon name="warning" /><span>{m.save_validity_longer()}</span></div>
  {/if}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
  <div class="lists" role="region" aria-label={m.save_changes_label()} tabindex="0">
    {#if rules.length > 0}
      <section>
        <h3>{m.save_rules_heading()}</h3>
        {#each rules as change (`${change.kind}/${change.rule.id}`)}
          {@const text = ruleText(change.rule, catalog, ctx.locale)}
          <div class="rule">
            <span class="tag">{TAGS[change.kind]()}</span>
            <span class="what">
              <RuleSentence {text} decision={change.rule.decision} size="sm" critical={change.rule.allow_critical === true} />
              {#if text.conditions}<span class="conditions">{text.conditions}</span>{/if}
            </span>
          </div>
        {/each}
      </section>
    {/if}
    {#if settings.length > 0}
      <section>
        <h3>{m.save_settings_heading()}</h3>
        {#each settings as line (line.setting)}
          <div class="rule">
            <span class="what">
              <strong>{line.label}</strong>{#if line.from || line.to}: <bdi>{line.from}</bdi> → <bdi>{line.to}</bdi>{/if}
            </span>
          </div>
        {/each}
      </section>
    {/if}
    <section>
      <h3>{m.save_effects_heading()}</h3>
      <EffectsList prev={prev.draft} next={next.draft} devices={catalog.devices} locale={ctx.locale} {unknown} />
    </section>
  </div>
  <p class="error" role="alert">{#if error}<Icon name="warning" size={16} />{error}{/if}</p>
  <div class="actions">
    <Button bind:element={back} size="lg" onclick={onclose}>{m.save_back()}</Button>
    <Button variant={critical ? 'danger' : 'primary'} size="lg" {busy} onclick={onconfirm}>{confirmLabel}</Button>
  </div>
</Dialog>

<style>
  .intro {
    display: flex;
    flex-direction: column;
    gap: 6px;
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
    overflow-wrap: anywhere;
  }
  .flag {
    display: flex;
    gap: var(--hm-space-2);
    padding: 10px var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-danger-fg);
    background: var(--hm-color-danger-bg);
    border: var(--hm-border-width) solid var(--hm-color-danger-border);
  }
  .longer {
    color: var(--hm-color-warning-fg);
    background: var(--hm-color-warning-bg);
    border-color: var(--hm-color-warning-border);
  }
  .lists {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    max-block-size: 50dvh;
    overflow-y: auto;
  }
  section {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  h3 {
    margin: 0;
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-text-muted);
  }
  .rule {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hm-space-1) 10px;
    padding-block: var(--hm-space-2);
    border-block-start: var(--hm-border-width) solid var(--hm-color-border-subtle);
  }
  .tag {
    min-inline-size: 92px;
    font-size: var(--hm-font-size-xs);
    font-weight: var(--hm-font-weight-semibold);
    color: var(--hm-color-accent-text);
  }
  .what {
    flex: 1 1 240px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--hm-font-size-sm);
    overflow-wrap: anywhere;
  }
  .what > :global(span),
  .what > strong {
    display: inline;
  }
  strong {
    font-weight: var(--hm-font-weight-semibold);
  }
  .conditions {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
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
