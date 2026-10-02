<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  First steps for a household without agents (design README 6.1, state "empty"): connect an
  agent, then create a mandate. The second step stays locked until the first is done, and
  says why; until then everything is denied.
-->
<script lang="ts">
  import { m } from '../../i18n.ts';
  import { href } from '../../router.ts';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';

  const id = $props.id();
  const TOTAL = 2;
</script>

<section class="welcome" aria-labelledby="{id}-title">
  <div class="intro">
    <span class="progress">{m.onboarding_progress({ done: 0, total: TOTAL })}</span>
    <h2 id="{id}-title">{m.onboarding_title()}</h2>
    <p>{m.onboarding_body()}</p>
  </div>
  <ol role="list">
    <li>
      <span class="step" aria-hidden="true">1</span>
      <div class="text"><strong>{m.onboarding_step1_title()}</strong><span>{m.onboarding_step1_body()}</span></div>
      <a class="primary" href={href({ name: 'pair' })}><Icon name="plus" />{m.onboarding_step1_action()}</a>
    </li>
    <li>
      <span class="step" aria-hidden="true">2</span>
      <div class="text"><strong>{m.onboarding_step2_title()}</strong><span>{m.onboarding_step2_body()}</span></div>
      <div class="locked">
        <Button disabled aria-describedby="{id}-lock">{m.onboarding_step2_action()}</Button>
        <span id="{id}-lock"><Icon name="lock" size={16} />{m.onboarding_step2_locked()}</span>
      </div>
    </li>
  </ol>
  <p class="deny"><Icon name="deny" size={16} />{m.onboarding_default_deny()}</p>
</section>

<style>
  .welcome {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-4);
    padding: var(--hm-space-5);
    border: var(--hm-border-width) solid var(--hm-color-border-subtle);
    border-radius: var(--hm-radius-lg);
    background: var(--hm-color-surface);
  }
  .intro {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-2);
  }
  .progress,
  .text span,
  .locked span,
  .deny {
    color: var(--hm-color-text-muted);
    font-size: var(--hm-font-size-sm);
  }
  h2,
  p {
    margin: 0;
  }
  ol {
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
  }
  li {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--hm-space-3);
  }
  @media (max-width: 767px) {
    li {
      grid-template-columns: auto minmax(0, 1fr);
    }
    li > :last-child {
      grid-column: 1 / -1;
    }
  }
  .step {
    display: grid;
    place-items: center;
    inline-size: 28px;
    block-size: 28px;
    border-radius: var(--hm-radius-pill);
    background: var(--hm-color-surface-sunken);
    font-weight: 600;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .primary {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-2);
    min-block-size: 44px;
    padding-inline: var(--hm-space-4);
    border-radius: var(--hm-radius-md);
    background: var(--hm-color-accent);
    color: var(--hm-color-text-on-accent);
    font-weight: 600;
    text-decoration: none;
  }
  .primary:hover {
    background: var(--hm-color-accent-hover);
  }
  .primary:focus-visible {
    outline: 2px solid var(--hm-color-focus);
    outline-offset: 2px;
  }
  .locked {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hm-space-1);
  }
  .locked span,
  .deny {
    display: inline-flex;
    align-items: center;
    gap: var(--hm-space-1);
  }
</style>
