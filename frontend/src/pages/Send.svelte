<script lang="ts">
  import { encrypt } from '../lib/crypto.ts';
  import type { Recipient } from '../lib/keylookup.ts';
  import { ApiError, maxCiphertext, siteBase, upload, type Uploaded } from '../lib/api.ts';
  import { filebinKeepsDays, filebinUpload, newBin } from '../lib/filebin.ts';
  import { deleteLink, filebinLink, shareLink } from '../lib/route.ts';
  import { formatBytes, formatDate } from '../lib/format.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import KeyPicker from '../components/KeyPicker.svelte';
  import Info from '../components/Info.svelte';
  import SecretField from '../components/SecretField.svelte';

  // The server limit is 100 MiB of ciphertext; OpenPGP adds a little.
  const maxFileSize = 100_000_000;

  // Where the ciphertext is kept (docs/storage/filebin.md). Only the
  // choice is sent; whether Filebin works is up to the server, which
  // refuses with 503 before any data is sent.
  type Where = 'local' | 'filebin' | 'filebin-direct';
  const shortExpiry = [
    { value: '1h', label: '1 hour' },
    { value: '24h', label: '1 day' },
    { value: '72h', label: '3 days' },
  ];
  // Filebin keeps bins about 7 days; the server allows 6 there.
  const expiryOptions: Record<'local' | 'filebin', { value: string; label: string }[]> = {
    local: [...shortExpiry, { value: '168h', label: '7 days' }],
    filebin: [...shortExpiry, { value: '144h', label: '6 days' }],
  };
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
  // Raw: OpenPGP key objects must not be wrapped in proxies.
  let recipients = $state.raw<Recipient[]>([]);

  let where = $state<Where>('local');
  let expires = $state('24h');
  let maxDownloads = $state(0);
  const expiries = $derived(expiryOptions[where === 'filebin' ? 'filebin' : 'local']);
  $effect(() => {
    if (!expiries.some((o) => o.value === expires)) expires = expiries.at(-1)!.value;
  });

  let busy = $state(false);
  let sent = $state(0);
  let error = $state('');
  let result = $state<Uploaded | null>(null);
  /** The bin of a direct upload to Filebin. */
  let directBin = $state('');
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

  // The button stays enabled and says what is missing, with the focus on
  // the field to fix.
  function missing(): { message: string; focus: string } | null {
    if (!contentReady) {
      if (what === 'text') return { message: 'Type a message first.', focus: '#send-content' };
      if (!file) return { message: 'Choose a file first.', focus: '#send-content' };
      return { message: `This file is ${formatBytes(file.size)}; the limit is 100 MB.`, focus: '#send-content' };
    }
    if (!recipientsReady) {
      if (mode === 'password') return { message: 'Choose a password first, or generate one.', focus: '#send-password' };
      if (recipients.length === 0) return { message: "Add the recipient's public key first.", focus: '[role="tab"][aria-selected="true"]' };
      return { message: 'Remove the keys that can no longer be used (expired or revoked).', focus: '.recipients button' };
    }
    return null;
  }

  let tried = $state(false);
  const gap = $derived(tried ? missing() : null);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    const todo = missing();
    if (todo) {
      tried = true;
      document.querySelector<HTMLElement>(todo.focus)?.focus();
      return;
    }
    busy = true;
    error = '';
    sent = 0;
    controller = new AbortController();
    try {
      const plaintext =
        what === 'text' ? { kind: 'text' as const, text } : { kind: 'file' as const, name: file!.name, data: file!.stream() };
      const to = mode === 'password' ? { password } : { publicKeys: recipients.map((r) => r.info.key) };
      const ciphertext = await encrypt(plaintext, to);
      if (where === 'filebin-direct') {
        // One request without progress events: those would make it a
        // preflighted request, which Filebin does not allow.
        const blob = await new Response(ciphertext).blob();
        if (blob.size > maxCiphertext) throw new TooLarge();
        const bin = newBin();
        await filebinUpload(bin, blob, controller.signal);
        directBin = bin;
      } else {
        const storage = where === 'filebin' ? 'filebin' : undefined;
        result = await upload(ciphertext, { expires, maxDownloads, storage }, (n) => (sent = n), controller.signal);
      }
    } catch (err) {
      if (controller.signal.aborted) error = 'Upload cancelled.';
      else if (err instanceof TooLarge) error = 'The encrypted file is larger than 100 MB.';
      else if (err instanceof ApiError) error = `Upload failed: ${err.message}`;
      else if (where === 'filebin-direct') error = 'The upload to Filebin failed. Try again later, or store it on FileBunny.';
      else error = 'Encryption or upload failed. Check your connection and try again.';
    } finally {
      busy = false;
      controller = null;
    }
  }

  class TooLarge extends Error {}

  function startOver() {
    result = null;
    directBin = '';
    tried = false;
    text = '';
    file = null;
    password = '';
    sent = 0;
  }
</script>

{#if directBin}
  <section>
    <h1>Encrypted and uploaded to Filebin</h1>
    <CopyField label="Share link" value={filebinLink(siteBase(), directBin)} />
    {#if mode === 'password'}
      <p class="note">
        The link does not contain the password. Send the password through a different channel, for example by phone or
        another messenger.
      </p>
    {:else}
      <p class="note">Only the holders of the private keys can decrypt it.</p>
    {/if}
    <p>
      Filebin keeps it for about {filebinKeepsDays} days. Anyone with the link can download it as often as they like, and
      delete it. FileBunny does not know about this file.
    </p>
    <button type="button" onclick={startOver}>Share something else</button>
  </section>
{:else if result}
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
    {#if where === 'filebin'}
      <p class="note">Stored at Filebin through FileBunny; Filebin only sees this server.</p>
    {/if}
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
          <textarea id="send-content" bind:value={text} rows="8" spellcheck="false"></textarea>
        </label>
      {:else}
        <label class="field">
          <span>File (max 100 MB)</span>
          <input id="send-content" type="file" onchange={pickFile} />
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
        <SecretField id="send-password" label="Password" bind:value={password} />
      {:else}
        <KeyPicker bind:recipients />
      {/if}
    </fieldset>

    <fieldset disabled={busy}>
      <legend>3. Where, how long and how often?</legend>
      <div class="choice">
        <label><input type="radio" bind:group={where} value="local" /> On FileBunny</label>
        <label><input type="radio" bind:group={where} value="filebin" /> At Filebin, through FileBunny</label>
        <label><input type="radio" bind:group={where} value="filebin-direct" /> At Filebin, directly</label>
        <Info label="About the storage options">
          Filebin (filebin.net) is a free file sharing service. It only ever gets the encrypted data. Through FileBunny,
          Filebin sees this server instead of you, and the limits below still apply. Directly, your browser uploads to
          Filebin itself: Filebin sees your IP address and FileBunny does not know about the file.
          <a href="#/status">Storage status</a>
        </Info>
      </div>
      {#if where === 'filebin-direct'}
        <p class="note">
          Filebin sees your IP address and keeps the file for about {filebinKeepsDays} days. There is no download limit and
          no delete link: anyone with the link can download it, and delete it.
        </p>
      {:else}
        <div class="row">
          <label class="field">
            <span>Expires after</span>
            <select bind:value={expires}>
              {#each expiries as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
            </select>
          </label>
          <label class="field">
            <span>Can be downloaded</span>
            <select bind:value={maxDownloads}>
              {#each downloadOptions as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
            </select>
          </label>
        </div>
      {/if}
    </fieldset>

    <ErrorMessage error={gap?.message ?? error} />
    {#if busy && where === 'filebin-direct'}
      <progress></progress>
      <button type="button" onclick={() => controller?.abort()}>Cancel</button>
    {:else if busy}
      <progress max={total} value={Math.min(sent, total)}></progress>
      <button type="button" onclick={() => controller?.abort()}>Cancel</button>
    {:else}
      <button type="submit" class="primary">Encrypt and upload</button>
    {/if}
  </form>
{/if}
