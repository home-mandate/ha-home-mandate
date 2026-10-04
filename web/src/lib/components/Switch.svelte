<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  On/off switch (role="switch"); tone "danger" when switching on weakens a protection.
  Disabled stays focusable (aria-disabled), so its description says why. Controlled: it
  shows the checked prop and reports a click through onchange; the caller decides (a save
  that fails leaves the switch as the server has it).
-->
<script lang="ts">
  interface Props {
    checked: boolean;
    label: string;
    description?: string;
    tone?: 'accent' | 'danger';
    disabled?: boolean;
    onchange?: (checked: boolean) => void;
  }

  let { checked, label, description, tone = 'accent', disabled = false, onchange }: Props = $props();

  const id = $props.id();

  function toggle() {
    if (!disabled) onchange?.(!checked);
  }
</script>

<!-- The whole row is the hit area: a label activates the switch it contains. -->
<label class="row" class:disabled>
  <button
    type="button"
    role="switch"
    class={tone}
    aria-checked={checked}
    aria-disabled={disabled ? 'true' : undefined}
    aria-labelledby="{id}-label"
    aria-describedby={description ? `${id}-desc` : undefined}
    onclick={toggle}
  >
    <span class="knob"></span>
  </button>
  <span class="text">
    <span id="{id}-label" class="label">{label}</span>
    {#if description}<span id="{id}-desc" class="desc" class:on={checked}>{description}</span>{/if}
  </span>
</label>

<style>
  .row {
    cursor: pointer;
    display: flex;
    align-items: flex-start;
    gap: var(--hm-space-3);
    min-block-size: var(--hm-size-touch);
  }
  button {
    position: relative;
    flex-shrink: 0;
    inline-size: 44px;
    block-size: 26px;
    margin-block-start: 1px;
    padding: 0;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-pressed);
    border: var(--hm-border-width) solid var(--hm-color-border-strong);
    cursor: pointer;
  }
  .knob {
    position: absolute;
    inset-block-start: 2px;
    inset-inline-start: 2px;
    inline-size: 20px;
    block-size: 20px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface);
    box-shadow: var(--hm-shadow-sm);
    transition: inset-inline-start var(--hm-motion-duration-fast) var(--hm-motion-easing-standard);
  }
  button[aria-checked='true'] .knob {
    inset-inline-start: 20px;
  }
  .accent[aria-checked='true'] {
    background: var(--hm-color-accent);
    border-color: var(--hm-color-accent);
  }
  .danger[aria-checked='true'] {
    background: var(--hm-color-danger-solid);
    border-color: var(--hm-color-danger-solid);
  }
  .danger[aria-checked='true'] .knob {
    background: var(--hm-color-on-danger);
  }
  .disabled {
    cursor: not-allowed;
  }
  .disabled .label {
    color: var(--hm-color-text-disabled);
  }
  button[aria-disabled='true'] {
    cursor: not-allowed;
    opacity: 0.6;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .label {
    font-size: 15px;
    font-weight: var(--hm-font-weight-semibold);
  }
  .desc {
    font-size: var(--hm-font-size-xs);
    color: var(--hm-color-text-muted);
  }
  .danger ~ .text .desc.on {
    color: var(--hm-color-danger-fg);
  }
  @media (forced-colors: active) {
    .knob {
      border: 1px solid CanvasText;
    }
    button[aria-checked='true'] {
      forced-color-adjust: none;
      background: Highlight;
      border-color: Highlight;
    }
  }
</style>
