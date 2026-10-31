<script lang="ts">
  import { encrypt } from '../lib/crypto.ts';
  import type { Recipient } from '../lib/keylookup.ts';
  import { ApiError, maxCiphertext, siteBase, upload, type Uploaded } from '../lib/api.ts';
  import { filebinUpload, newBin } from '../lib/filebin.ts';
  import { storageByName, storages } from '../lib/storage.ts';
  import { deleteLink, filebinLink, shareLink } from '../lib/route.ts';
  import { formatBytes, formatDate } from '../lib/format.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import KeyPicker from '../components/KeyPicker.svelte';
  import Info from '../components/Info.svelte';
  import SecretField from '../components/SecretField.svelte';
  import StorageStatus from '../components/StorageStatus.svelte';

  // The server limit is 100 MiB of ciphertext; OpenPGP adds a little.
  const maxFileSize = 100_000_000;

  // Where the ciphertext is kept (docs/storage/), and whether it goes
  // there through FileBunny or straight from this browser. Only the choice
  // is sent; whether a service works is up to the server, which refuses
  // with 503 before any data is sent.
  const shortExpiry = [
    { value: '1h', label: '1 hour' },
    { value: '24h', label: '1 day' },
    { value: '72h', label: '3 days' },
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
  // Raw: OpenPGP key objects must not be wrapped in proxies.
  let recipients = $state.raw<Recipient[]>([]);

  let storageName = $state('local');
  let via = $state<'server' | 'direct'>('server');
  let statusOpen = $state(false);
  const chosen = $derived(storageByName(storageName)!);
  const direct = $derived(chosen.direct && via === 'direct');
  let expires = $state('24h');
  let maxDownloads = $state(0);
  const expiries = $derived([
    ...shortExpiry,
    { value: `${chosen.maxExpiryHours}h`, label: `${chosen.maxExpiryHours / 24} days` },
  ]);
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
      if (direct) {
        // Filebin is the only service with a direct route so far. One request without progress events: those would make it a
        // preflighted request, which Filebin does not allow.
        const blob = await new Response(ciphertext).blob();
        if (blob.size > maxCiphertext) throw new TooLarge();
        const bin = newBin();
        await filebinUpload(bin, blob, controller.signal);
        directBin = bin;
      } else {
        const storage = chosen.name === 'local' ? undefined : chosen.name;
        result = await upload(ciphertext, { expires, maxDownloads, storage }, (n) => (sent = n), controller.signal);
      }
    } catch (err) {
      if (controller.signal.aborted) error = 'Upload cancelled.';
      else if (err instanceof TooLarge) error = 'The encrypted file is larger than 100 MB.';
      else if (err instanceof ApiError) error = `Upload failed: ${err.message}`;
      else if (direct) error = `The upload to ${chosen.short} failed. Try again later, or upload through FileBunny.`;
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
    <h1>Encrypted and uploaded to {chosen.short}</h1>
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
      {chosen.short} keeps it for about {chosen.keepsDays} days. Anyone with the link can download it as often as they
      like, and delete it. FileBunny does not know about this file.
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
    {#if chosen.name !== 'local'}
      <p class="note">Stored at {chosen.short} through FileBunny; {chosen.short} only sees this server.</p>
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
      <label class="field">
        <span>Stored at</span>
        <select bind:value={storageName}>
          {#each storages as o (o.name)}<option value={o.name}>{o.label}</option>{/each}
        </select>
      </label>
      {#if chosen.direct}
        <div class="choice">
          <label><input type="radio" bind:group={via} value="server" /> Through FileBunny</label>
          <label><input type="radio" bind:group={via} value="direct" /> Directly from your browser</label>
          <Info label="About the two ways">
            {chosen.short} only ever gets the encrypted data. Through FileBunny, it sees this server instead of you, and
            the limits below still apply. Directly, your browser uploads to {chosen.short} itself: it sees your IP address
            and FileBunny does not know about the file.
          </Info>
        </div>
      {:else if chosen.name !== 'local'}
        <p class="note">Sent through FileBunny: {chosen.short} sees this server instead of you.</p>
      {/if}
      <details class="more" bind:open={statusOpen}>
        <summary>Storage status</summary>
        {#if statusOpen}<StorageStatus />{/if}
      </details>
      {#if direct}
        <p class="note">
          {chosen.short} sees your IP address and keeps the file for about {chosen.keepsDays} days. There is no download
          limit and no delete link: anyone with the link can download it, and delete it.
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
    {#if busy && direct}
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
