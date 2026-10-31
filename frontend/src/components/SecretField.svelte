<script lang="ts">
  // A password or passphrase field with Show/Hide, Generate and Copy.
  // Generate never reveals the value: Copy puts it in a password manager
  // without showing it on screen.
  import { copyText, generatePassword } from '../lib/format.ts';

  let { label, value = $bindable('') }: { label: string; value?: string } = $props();
  let show = $state(false);
  let copied = $state(false);

  async function copy() {
    copied = await copyText(value);
    if (copied) setTimeout(() => (copied = false), 2000);
  }
</script>

<label class="field">
  <span>{label}</span>
  <span class="row">
    <input type={show ? 'text' : 'password'} bind:value autocomplete="new-password" />
    <button type="button" onclick={() => (show = !show)}>{show ? 'Hide' : 'Show'}</button>
    <button type="button" onclick={() => (value = generatePassword())}>Generate</button>
    <button type="button" onclick={copy} disabled={!value}>{copied ? 'Copied' : 'Copy'}</button>
  </span>
</label>
