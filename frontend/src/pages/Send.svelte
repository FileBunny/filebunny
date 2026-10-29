<script lang="ts">
  import { encrypt } from '../lib/crypto.ts';
  import type { Recipient } from '../lib/keylookup.ts';
  import { ApiError, siteBase, upload, type Uploaded } from '../lib/api.ts';
  import { deleteLink, shareLink } from '../lib/route.ts';
  import { formatBytes, formatDate, generatePassword } from '../lib/format.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import KeyPicker from '../components/KeyPicker.svelte';

  // The server limit is 100 MiB of ciphertext; OpenPGP adds a little.
  const maxFileSize = 100_000_000;

  const expiryOptions = [
    { value: '1h', label: '1 hour' },
    { value: '24h', label: '1 day' },
    { value: '72h', label: '3 days' },
    { value: '168h', label: '7 days' },
  ];
  const downloadOptions = [
    { value: 1, label: 'Once' },
    { value: 5, label: '5 times' },
    { value: 10, label: '10 times' },
    { value: 0, label: 'Unlimited' },
  ];

  let what = $state<'text' | 'file'>('text');
  let text = $state('');
  let file = $state<File | null>(null);

  let mode = $state<'password' | 'key'>('password');
  let password = $state('');
  let showPassword = $state(false);
  // Raw: OpenPGP key objects must not be wrapped in proxies.
  let recipients = $state.raw<Recipient[]>([]);

  let expires = $state('24h');
  let maxDownloads = $state(0);

  let busy = $state(false);
  let sent = $state(0);
  let error = $state('');
  let result = $state<Uploaded | null>(null);
  let controller: AbortController | null = null;

  const total = $derived(what === 'file' ? (file?.size ?? 0) : new TextEncoder().encode(text).length);
  const contentReady = $derived(what === 'text' ? text.length > 0 : file !== null && file.size <= maxFileSize);
  const recipientsReady = $derived(
    mode === 'password' ? password.length > 0 : recipients.length > 0 && recipients.every((r) => r.info.canEncrypt),
  );

  function pickFile(e: Event & { currentTarget: HTMLInputElement }) {
    file = e.currentTarget.files?.[0] ?? null;
    error = file && file.size > maxFileSize ? `This file is ${formatBytes(file.size)}; the limit is 100 MB.` : '';
  }

  function newPassword() {
    password = generatePassword();
    showPassword = true;
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!contentReady || !recipientsReady || busy) return;
    busy = true;
    error = '';
    sent = 0;
    controller = new AbortController();
    try {
      const plaintext =
        what === 'text' ? { kind: 'text' as const, text } : { kind: 'file' as const, name: file!.name, data: file!.stream() };
      const to = mode === 'password' ? { password } : { publicKeys: recipients.map((r) => r.info.key) };
      const ciphertext = await encrypt(plaintext, to);
      result = await upload(ciphertext, { expires, maxDownloads }, (n) => (sent = n), controller.signal);
    } catch (err) {
      if (controller.signal.aborted) error = 'Upload cancelled.';
      else if (err instanceof ApiError) error = `Upload failed: ${err.message}`;
      else error = 'Encryption or upload failed. Check your connection and try again.';
    } finally {
      busy = false;
      controller = null;
    }
  }

  function startOver() {
    result = null;
    text = '';
    file = null;
    password = '';
    sent = 0;
  }
</script>

{#if result}
  <section>
    <h1>Encrypted and uploaded</h1>
    <CopyField label="Share link" value={shareLink(siteBase(), result.id)} />
    {#if mode === 'password'}
      <p class="note">
        The link does not contain the password. Send the password through a different channel, for example by phone or
        another messenger.
      </p>
    {:else}
      <p class="note">Only the holders of the private keys can decrypt it.</p>
    {/if}
    <p>
      Available until {formatDate(result.expiresAt)}{#if result.maxDownloads === 1}, for one download{:else if result.maxDownloads > 1},
        for {result.maxDownloads} downloads{/if}.
    </p>
    <CopyField label="Delete link (keep this to yourself)" value={deleteLink(siteBase(), result.id, result.deleteToken)} />
    <button type="button" onclick={startOver}>Share something else</button>
  </section>
{:else}
  <form onsubmit={submit}>
    <fieldset disabled={busy}>
      <legend>1. What do you want to share?</legend>
      <div class="choice">
        <label><input type="radio" bind:group={what} value="text" /> Text</label>
        <label><input type="radio" bind:group={what} value="file" /> File</label>
      </div>
      {#if what === 'text'}
        <label class="field">
          <span>Message</span>
          <textarea bind:value={text} rows="8" spellcheck="false"></textarea>
        </label>
      {:else}
        <label class="field">
          <span>File (max 100 MB)</span>
          <input type="file" onchange={pickFile} />
        </label>
        {#if file}<p class="note">{file.name}, {formatBytes(file.size)}</p>{/if}
      {/if}
    </fieldset>

    <fieldset disabled={busy}>
      <legend>2. Who may read it?</legend>
      <div class="choice">
        <label><input type="radio" bind:group={mode} value="password" /> Anyone with a password</label>
        <label><input type="radio" bind:group={mode} value="key" /> Holders of a public key</label>
      </div>
      {#if mode === 'password'}
        <label class="field">
          <span>Password</span>
          <span class="row">
            <input type={showPassword ? 'text' : 'password'} bind:value={password} autocomplete="new-password" />
            <button type="button" onclick={() => (showPassword = !showPassword)}>{showPassword ? 'Hide' : 'Show'}</button>
            <button type="button" onclick={newPassword}>Generate</button>
          </span>
        </label>
      {:else}
        <KeyPicker bind:recipients />
      {/if}
    </fieldset>

    <fieldset disabled={busy}>
      <legend>3. How long and how often?</legend>
      <div class="row">
        <label class="field">
          <span>Expires after</span>
          <select bind:value={expires}>
            {#each expiryOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
          </select>
        </label>
        <label class="field">
          <span>Can be downloaded</span>
          <select bind:value={maxDownloads}>
            {#each downloadOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
          </select>
        </label>
      </div>
    </fieldset>

    <ErrorMessage {error} />
    {#if busy}
      <progress max={total} value={Math.min(sent, total)}></progress>
      <button type="button" onclick={() => controller?.abort()}>Cancel</button>
    {:else}
      <button type="submit" class="primary" disabled={!contentReady || !recipientsReady}>Encrypt and upload</button>
    {/if}
  </form>
{/if}
