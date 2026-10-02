<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<!-- Sections whose screens follow in the next steps show their title only. -->
<script lang="ts">
  import type { Section } from '../router.ts';
  import { m } from '../i18n.ts';
  import PageHeader from '../components/PageHeader.svelte';

  interface Props {
    section: Section;
  }

  let { section }: Props = $props();

  const TITLES: Record<Section, () => string> = {
    overview: () => m.overview_title(),
    agents: () => m.agents_title(),
    mandates: () => m.mandates_title(),
    audit: () => m.audit_title(),
    settings: () => m.settings_title(),
  };
</script>

<PageHeader title={TITLES[section]()} />
