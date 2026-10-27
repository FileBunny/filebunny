<script lang="ts">
  import { parseRoute } from './lib/route.ts';
  import Send from './pages/Send.svelte';
  import Download from './pages/Download.svelte';
  import Delete from './pages/Delete.svelte';

  let route = $state(parseRoute(location.hash));

  $effect(() => {
    const update = () => (route = parseRoute(location.hash));
    window.addEventListener('hashchange', update);
    return () => window.removeEventListener('hashchange', update);
  });
</script>

<header>
  <a href="#/" class="brand">FileBunny</a>
  <span class="tagline">Encrypt in your browser with OpenPGP, share anonymously.</span>
</header>

<main>
  {#if route.page === 'send'}
    <Send />
  {:else if route.page === 'download'}
    {#key route.id}
      <Download id={route.id} />
    {/key}
  {:else if route.page === 'delete'}
    {#key route.id}
      <Delete id={route.id} token={route.token} />
    {/key}
  {:else}
    <h1>Page not found</h1>
    <p>Check the link, or <a href="#/">share something new</a>.</p>
  {/if}
</main>

<footer>
  Everything is encrypted in this browser before it is uploaded. The server only stores ciphertext and no IP addresses.
</footer>
