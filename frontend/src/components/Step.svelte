<script lang="ts">
  // One step of a form. Open, it shows its fields and a button to go on;
  // closed, one line of summary and a Change button. The fields stay in
  // the page while closed (hidden), so nothing in them is lost. The page
  // decides which step is open.
  import type { Snippet } from 'svelte';

  let {
    n,
    title,
    open,
    summary,
    disabled = false,
    next = 'Continue',
    onopen,
    onnext,
    children,
  }: {
    n: number;
    title: string;
    open: boolean;
    summary: string;
    disabled?: boolean;
    next?: string;
    onopen: () => void;
    onnext: () => void;
    children: Snippet;
  } = $props();

  const id = $derived(`step-${n}`);
</script>

<fieldset class="step" class:open {disabled}>
  <legend>{n}. {title}</legend>
  {#if !open}
    <div class="step-summary">
      <span>{summary}</span>
      <button type="button" aria-expanded="false" aria-controls={id} onclick={onopen}>Change</button>
    </div>
  {/if}
  <div {id} hidden={!open}>
    {@render children()}
    <div class="step-next">
      <button type="button" onclick={onnext}>{next}</button>
    </div>
  </div>
</fieldset>
