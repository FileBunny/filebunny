<script lang="ts">
  // The storage status on its own page, for a link to #/status.
  import StorageStatus from '../components/StorageStatus.svelte';

  function back(e: MouseEvent) {
    // Back to where the visitor came from, with what they entered there;
    // a fresh tab has nowhere to go back to and follows the link.
    if (history.length > 1) {
      e.preventDefault();
      history.back();
    }
  }
</script>

<h1>Storage status</h1>
<StorageStatus />
<p><a href="#/" onclick={back}>Back</a></p>
