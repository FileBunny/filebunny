<script lang="ts">
  import KeyGen from '../components/KeyGen.svelte';
</script>

<h1>Create a key pair</h1>
<p>
  A key pair lets people send you things only you can open: share the public key, keep the private key. Both are
  created in this browser; the private key is never uploaded.
</p>
<KeyGen />
<p class="note">Want people to send you files? <a href="#/receive">Create an upload link</a> with this key.</p>
