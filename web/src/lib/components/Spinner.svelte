<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Busy indicator inside buttons: a ring with an open end, decorative only. -->
<span class="spinner" aria-hidden="true"></span>

<style>
  .spinner {
    inline-size: 14px;
    block-size: 14px;
    flex-shrink: 0;
    border-radius: var(--hm-radius-pill);
    border: 2px solid currentColor;
    border-inline-end-color: transparent;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .spinner {
      animation: none;
    }
  }
</style>
