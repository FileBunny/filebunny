<script lang="ts">
  // One step of a form that goes step by step. The open step shows where
  // you are ("Step 2 of 3" and a bar), its fields, Back on the left and
  // Next on the right (on the last step: the submit button). An earlier
  // step shows one line of summary and Change; a later step is not shown.
  // Fields stay in the page while hidden, so nothing in them is lost.
  import type { Snippet } from 'svelte';
  import ErrorMessage from './ErrorMessage.svelte';

  let {
    n,
    total,
    current,
    title,
    summary,
    disabled = false,
    submitLabel = 'Submit',
    error = '',
    onchange,
    onback,
    onnext,
    children,
  }: {
    n: number;
    total: number;
    /** The open step. */
    current: number;
    /** What is still missing in this step, shown just above Back and Next. */
    error?: string;
    title: string;
    summary: string;
    disabled?: boolean;
    /** The submit button of the last step. */
    submitLabel?: string;
    onchange: () => void;
    onback: () => void;
    onnext: () => void;
    children: Snippet;
  } = $props();

  const open = $derived(n === current);
  const last = $derived(n === total);
  const id = $derived(`step-${n}`);
</script>

<fieldset class="step" class:open hidden={n > current} {disabled}>
  <legend>{n}. {title}</legend>
  {#if open}
    <div class="step-progress">
      <span>Step {n} of {total}</span>
      <span class="step-bar" aria-hidden="true">
        {#each { length: total } as _, i (i)}<span class:done={i < n}></span>{/each}
      </span>
    </div>
  {:else}
    <div class="step-summary">
      <span>{summary}</span>
      <button type="button" aria-controls={id} onclick={onchange}>Change</button>
    </div>
  {/if}
  <div {id} hidden={!open}>
    {@render children()}
    <ErrorMessage {error} />
    <div class="step-nav">
      {#if n > 1}<button type="button" onclick={onback}>Back</button>{/if}
      {#if last}
        <button type="submit" class="primary" id="{id}-next">{submitLabel}</button>
      {:else}
        <button type="button" class="primary" id="{id}-next" onclick={onnext}>Next</button>
      {/if}
    </div>
  </div>
</fieldset>
