<script lang="ts">
  // A fingerprint as a QR code (OPENPGP4FPR:…), behind "Show as QR code",
  // to compare it with a phone or let an OpenPGP app find the key. Drawn
  // as one SVG path: no HTML strings, nothing loaded.
  import { fingerprintUri, qrModules } from '../lib/qr.ts';

  let { fingerprint }: { fingerprint: string } = $props();
  let open = $state(false);

  // Four modules of quiet zone around the code, as scanners need.
  const quiet = 4;
  const modules = $derived(open ? qrModules(fingerprintUri(fingerprint)) : []);
  const size = $derived(modules.length + 2 * quiet);
  const path = $derived(
    modules
      .flatMap((row, r) => row.map((dark, c) => (dark ? `M${c + quiet},${r + quiet}h1v1h-1z` : '')))
      .join(''),
  );
</script>

<details class="more qr" bind:open>
  <summary>Show as QR code</summary>
  {#if open}
    <svg viewBox="0 0 {size} {size}" role="img" aria-label="QR code of fingerprint {fingerprint}" shape-rendering="crispEdges">
      <rect width={size} height={size} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
    <p class="note">Scan it with a phone to compare the fingerprint, or with an OpenPGP app to find the key.</p>
  {/if}
</details>
