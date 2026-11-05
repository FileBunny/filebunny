<script lang="ts">
  // A password or passphrase field with Show/Hide, Generate and Copy.
  // Generate never reveals the value: Copy puts it in a password manager
  // without showing it on screen. With shortWarning, a value under
  // minPasswordLength shows that warning (it does not block).
  import { copyText, generatePassword, minPasswordLength } from '../lib/format.ts';

  let {
    label,
    value = $bindable(''),
    id,
    shortWarning = '',
  }: { label: string; value?: string; id?: string; shortWarning?: string } = $props();
  const short = $derived(shortWarning !== '' && value.length > 0 && value.length < minPasswordLength);
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
    <input {id} type={show ? 'text' : 'password'} bind:value autocomplete="new-password" />
    <button type="button" onclick={() => (show = !show)}>{show ? 'Hide' : 'Show'}</button>
    <button type="button" onclick={() => (value = generatePassword())}>Generate</button>
    <button type="button" onclick={copy} disabled={!value}>{copied ? 'Copied' : 'Copy'}</button>
  </span>
</label>
{#if short}
  <p class="notice">{shortWarning}</p>
{/if}
