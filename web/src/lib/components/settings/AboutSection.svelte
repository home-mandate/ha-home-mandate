<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!--
  About (design README 6.11 section 7; decision S8): version and commit, the license with a
  link to the source code (AGPL-3.0 section 13 asks to offer it to everyone using the
  program over a network), and the licenses of the included packages (built with the page).
-->
<script lang="ts">
  import { m } from '../../i18n.ts';

  interface Props {
    version: string;
    commit: string;
  }

  let { version, commit }: Props = $props();

  const SOURCE_URL = 'https://github.com/home-mandate/ha-home-mandate';
  const LICENSE = 'AGPL-3.0-or-later';
</script>

<div class="about">
  <img src="./hm-logo-128.png" alt="" width="72" height="72" />
  <div class="text">
    <p>{m.set_about_desc()}</p>
    <dl>
      <dt>{m.set_version()}</dt>
      <dd class="mono">{version}</dd>
      <dt>{m.set_commit()}</dt>
      <dd class="mono"><span dir="ltr">{commit}</span></dd>
      <dt>{m.set_license()}</dt>
      <dd>{LICENSE} · <a href={SOURCE_URL} target="_blank" rel="noopener noreferrer">{m.set_source()}<span class="hm-visually-hidden"> {m.common_new_tab()}</span></a></dd>
      <dt>{m.set_licenses()}</dt>
      <dd><a href="./licenses.txt" target="_blank" rel="noopener">{m.set_licenses_link()}<span class="hm-visually-hidden"> {m.common_new_tab()}</span></a></dd>
    </dl>
  </div>
</div>

<style>
  .about {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hm-space-5);
    align-items: flex-start;
  }
  .text {
    display: flex;
    flex-direction: column;
    gap: var(--hm-space-3);
    flex: 1 1 260px;
  }
  p {
    margin: 0;
  }
  dl {
    display: grid;
    grid-template-columns: fit-content(45%) minmax(0, 1fr);
    gap: var(--hm-space-2) var(--hm-space-4);
    margin: 0;
  }
  dt {
    color: var(--hm-color-text-muted);
    overflow-wrap: anywhere;
  }
  dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .mono {
    font-family: var(--hm-font-mono);
  }
  a {
    color: var(--hm-color-accent-text);
    font-weight: var(--hm-font-weight-medium);
  }
</style>
