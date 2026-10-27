<script lang="ts">
  let { error }: { error: string } = $props();
</script>

{#if error}
  <p class="error" role="alert">{error}</p>
{/if}
