<script lang="ts">
  // The proof of work while it runs (see lib/pow.ts): a few seconds on a
  // phone without a JavaScript JIT, so the page says what it is doing.
  // The share is honest: the work is 16 small puzzles.
  let { done }: { done: number } = $props();
  const percent = $derived(Math.round(done * 100));
</script>

<p class="note" role="status">Proof of work against abuse, done in this browser…</p>
<progress max="100" value={percent} aria-label="Proof of work">{percent}%</progress>
