<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  Defaults (design README 6.11 section 2; decisions F5, S5): approval timeout and rate
  limit for new templates and mandates, saved 400 ms after the last change when valid, and
  the language of the interface for the signed-in person. Invalid input is shown and not
  saved; the status says when a save went through or failed.
-->
<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import type { Defaults, Language } from '../../api/types.ts';
  import { timeoutSeconds } from '../../engine/check.ts';
  import { m } from '../../i18n.ts';
  import Icon from '../Icon.svelte';
  import SelectField from '../SelectField.svelte';
  import TimeoutField from '../TimeoutField.svelte';

  interface Props {
    defaults: Defaults;
    /** The person's language setting; null means the browser's. */
    language: Language | null;
    /** The language the browser would give, for "Automatic (browser: …)". */
    browserLanguage: Language;
    onsave: (patch: Partial<Defaults>) => Promise<void>;
    onlanguage: (language: Language | null) => Promise<void>;
  }

  let { defaults, language, browserLanguage, onsave, onlanguage }: Props = $props();

  const id = $props.id();
  const SAVE_DELAY_MS = 400;
  const TIMEOUT_MIN_S = 10;
  const TIMEOUT_MAX_S = 3600;
  const RATE_MIN = 1;
  const RATE_MAX = 1000;

  let timeout = $state(untrack(() => defaults.approval_timeout));
  let rate: number | null = $state(untrack(() => defaults.max_actions_per_hour));
  let status = $state<'idle' | 'saved' | 'failed'>('idle');
  let timer: ReturnType<typeof setTimeout> | undefined;
  onDestroy(() => clearTimeout(timer));

  const seconds = $derived(timeoutSeconds(timeout));
  const timeoutError = $derived(Number.isNaN(seconds) || seconds < TIMEOUT_MIN_S || seconds > TIMEOUT_MAX_S ? m.timeout_error() : '');
  const rateError = $derived(rate === null || !Number.isInteger(rate) || rate < RATE_MIN || rate > RATE_MAX ? m.set_rate_error() : '');
  const languages = $derived([
    { value: '', label: m.set_ui_lang_auto({ lang: browserLanguage === 'de' ? m.lang_de() : m.lang_en() }) },
    { value: 'de', label: m.lang_de() },
    { value: 'en', label: m.lang_en() },
  ]);

  /** schedule saves the valid fields after a pause; invalid ones wait for a correction. */
  function schedule() {
    status = 'idle';
    clearTimeout(timer);
    timer = setTimeout(save, SAVE_DELAY_MS);
  }

  async function save() {
    const patch: Partial<Defaults> = {};
    if (!timeoutError && timeout !== defaults.approval_timeout) patch.approval_timeout = timeout;
    if (!rateError && rate !== null && rate !== defaults.max_actions_per_hour) patch.max_actions_per_hour = rate;
    if (Object.keys(patch).length === 0) return;
    try {
      await onsave(patch);
      status = 'saved';
    } catch {
      status = 'failed';
    }
  }

  async function pickLanguage(value: string) {
    try {
      await onlanguage((value || null) as Language | null);
    } catch {
      status = 'failed';
    }
  }
</script>

<p class="desc">{m.settings_autosave()}</p>
<div class="fields">
  <TimeoutField
    {timeout}
    error={timeoutError}
    help={m.set_approvals_desc()}
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
  <SelectField label={m.set_ui_lang()} value={language ?? ''} options={languages} onchange={(v) => void pickLanguage(v)} />
</div>
<p class="status" role="status">
  {#if status === 'saved'}<Icon name="check" size={16} />{m.set_saved()}{:else if status === 'failed'}<Icon name="warning" size={16} />{m.set_save_failed()}{/if}
</p>

<style>
  .desc {
    margin: 0;
    color: var(--hm-color-text-muted);
  }
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
    outline: 1px solid var(--hm-color-danger-fg);
    outline-offset: 0;
  }
  .unit,
  .help {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-subtle);
  }
  .help {
    display: flex;
    gap: 6px;
  }
  .help.error {
    color: var(--hm-color-danger-fg);
  }
  .status {
    display: flex;
    gap: 6px;
    min-block-size: 1.5em;
    margin: 0;
    font-size: var(--hm-font-size-sm);
    color: var(--hm-color-text-muted);
  }
</style>
