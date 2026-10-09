<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Defaults (design README 6.11 section 2; decisions F5, S5): approval timeout and rate
  limit for new templates and mandates, saved 400 ms after the last valid change, and the
  language of the interface for the signed-in person. A field follows changes made
  elsewhere unless an edit of it waits; an edit that waits is saved before leaving the
  page or switching the language. Invalid input is shown and not saved; a failed save can
  be retried. The language is applied with its own button, because the page reloads.
-->
<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import type { Defaults, Language } from '../../api/types.ts';
  import { timeoutSeconds } from '../../engine/check.ts';
  import { m } from '../../i18n.ts';
  import { toasts } from '../../ui/toasts.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import TimeoutField from '../TimeoutField.svelte';

  interface Props {
    defaults: Defaults;
    /** The installation's upper limit for the approval timeout in seconds; null while unknown. */
    maxTimeout?: number | null;
    /** The person's language setting; null means the browser's. */
    language: Language | null;
    /** The language the browser would give, for "Automatic (browser: …)". */
    browserLanguage: Language;
    onsave: (patch: Partial<Defaults>) => Promise<void>;
    onlanguage: (language: Language | null) => Promise<void>;
  }

  let { defaults, maxTimeout = null, language, browserLanguage, onsave, onlanguage }: Props = $props();

  const id = $props.id();
  const SAVE_DELAY_MS = 400;
  const SAVED_SHOWN_MS = 4000;
  const TIMEOUT_MIN_S = 10;
  const TIMEOUT_MAX_S = 3600;
  const RATE_MIN = 1;
  const RATE_MAX = 1000;

  let timeout = $state(untrack(() => defaults.approval_timeout));
  let rate: number | null = $state(untrack(() => defaults.max_actions_per_hour));
  let chosenLanguage = $state(untrack(() => language ?? ''));
  let status = $state<'idle' | 'saved' | 'failed'>('idle');
  /** An edit waits for the pause (or failed): the field does not follow the server then. */
  let dirty = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let shown: ReturnType<typeof setTimeout> | undefined;

  // Changes made elsewhere (another admin, another tab) show unless an edit waits.
  $effect(() => {
    const { approval_timeout, max_actions_per_hour } = defaults;
    untrack(() => {
      if (dirty) return;
      timeout = approval_timeout;
      rate = max_actions_per_hour;
    });
  });

  // Leaving the page within the pause still saves (the save outlives the component); a
  // failure then shows as a toast, since the field's status is gone with the page.
  let leaving = false;
  onDestroy(() => {
    clearTimeout(shown);
    leaving = true;
    if (timer !== undefined) void flush();
  });

  const seconds = $derived(timeoutSeconds(timeout));
  const timeoutError = $derived(Number.isNaN(seconds) || seconds < TIMEOUT_MIN_S || seconds > TIMEOUT_MAX_S ? m.timeout_error() : '');
  const rateError = $derived(rate === null || !Number.isInteger(rate) || rate < RATE_MIN || rate > RATE_MAX ? m.set_rate_error() : '');
  const languages = $derived([
    { value: '', label: m.set_ui_lang_auto({ lang: browserLanguage === 'de' ? m.lang_de() : m.lang_en() }) },
    { value: 'de', label: m.lang_de(), lang: 'de' },
    { value: 'en', label: m.lang_en(), lang: 'en' },
  ]);

  /** schedule saves the valid fields after a pause; invalid ones wait for a correction. */
  function schedule() {
    dirty = true;
    status = 'idle';
    clearTimeout(timer);
    timer = setTimeout(() => void flush(), SAVE_DELAY_MS);
  }

  async function flush() {
    clearTimeout(timer);
    timer = undefined;
    const patch: Partial<Defaults> = {};
    if (!timeoutError && timeout !== defaults.approval_timeout) patch.approval_timeout = timeout;
    if (!rateError && rate !== null && rate !== defaults.max_actions_per_hour) patch.max_actions_per_hour = rate;
    if (Object.keys(patch).length === 0) {
      dirty = Boolean(timeoutError || rateError);
      return;
    }
    try {
      await onsave(patch);
      dirty = Boolean(timeoutError || rateError);
      status = 'saved';
      clearTimeout(shown);
      shown = setTimeout(() => {
        if (status === 'saved') status = 'idle';
      }, SAVED_SHOWN_MS);
    } catch {
      if (leaving) toasts.show({ kind: 'error', text: m.set_save_failed() });
      else status = 'failed';
    }
  }

  async function applyLanguage() {
    if (timer !== undefined) await flush();
    try {
      await onlanguage(chosenLanguage === 'de' || chosenLanguage === 'en' ? chosenLanguage : null);
    } catch {
      status = 'failed';
    }
  }
</script>

<div class="fields">
  <TimeoutField
    {timeout}
    error={timeoutError}
    help={m.set_approvals_desc()}
    max={maxTimeout}
    onchange={(t) => {
      timeout = t;
      schedule();
    }}
  />
  <div class="field">
    <label for="{id}-rate">{m.set_rate()}</label>
    <div class="rate">
      <input
        id="{id}-rate"
        type="number"
        inputmode="numeric"
        min={RATE_MIN}
        max={RATE_MAX}
        bind:value={rate}
        aria-invalid={rateError ? 'true' : undefined}
        aria-describedby="{id}-rate-help {id}-rate-unit"
        oninput={schedule}
      />
      <span id="{id}-rate-unit" class="unit">{m.set_rate_label()}</span>
    </div>
    <span id="{id}-rate-help" class="help" class:error={rateError}>
      {#if rateError}<Icon name="warning" size={16} />{rateError}{:else}{m.set_rate_desc()}{/if}
    </span>
  </div>
  <div class="language">
    <SelectField label={m.set_ui_lang()} value={chosenLanguage} options={languages} help={m.set_ui_lang_help()} onchange={(v) => (chosenLanguage = v)} />
    <Button disabled={chosenLanguage === (language ?? '')} onclick={applyLanguage}>{m.common_apply()}</Button>
  </div>
</div>
<div class="status">
  <p role="status">
    {#if status === 'saved'}<Icon name="check" size={16} />{m.set_saved()}{:else if status === 'failed'}<Icon name="warning" size={16} />{m.set_save_failed()}{/if}
  </p>
  {#if status === 'failed'}<Button variant="text" onclick={() => void flush()}>{m.common_retry()}</Button>{/if}
</div>

<style>
  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
    gap: var(--hm-space-4);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  label {
    font-size: var(--hm-font-size-sm);
    font-weight: var(--hm-font-weight-medium);
  }
  .rate {
    display: flex;
    align-items: center;
    gap: var(--hm-space-2);
  }
  input {
    inline-size: 104px;
    box-sizing: border-box;
    min-block-size: var(--hm-size-touch);
    padding-inline: var(--hm-space-3);
    border-radius: var(--hm-radius-md);
    font: inherit;
    font-size: var(--hm-font-size-md);
    color: var(--hm-color-text);
    background: var(--hm-color-surface);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
  }
  input[aria-invalid='true'] {
    border-color: var(--hm-color-danger-fg);
    /* A shadow, not an outline: the outline stays free for the focus ring. */
    box-shadow: 0 0 0 1px var(--hm-color-danger-fg);
  }
  .unit,
  .help {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .help {
    display: flex;
    gap: 6px;
  }
  .help.error {
    color: var(--hm-color-danger-fg);
  }
  .language {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-2);
  }
  .language > :global(.field) {
    align-self: stretch;
  }
  .status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hm-space-3);
    min-block-size: 1.5em;
  }
  .status p {
    display: flex;
    gap: 6px;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
</style>
