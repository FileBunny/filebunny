<script lang="ts">
  // Background information behind a small ⓘ, so the page shows only what
  // you need before you click. A <details> element: works without script
  // and with keyboard and screen readers.
  import type { Snippet } from 'svelte';

  let { label, children }: { label: string; children: Snippet } = $props();
</script>

<details class="info">
  <summary aria-label={label} title={label}>ⓘ</summary>
  <span class="info-text">{@render children()}</span>
</details>
