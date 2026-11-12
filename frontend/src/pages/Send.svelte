<script lang="ts">
  import { encrypt, unusableText } from '../lib/crypto.ts';
  import type { Recipient } from '../lib/keylookup.ts';
  import { ApiError, maxCiphertext, siteBase, upload, type Uploaded } from '../lib/api.ts';
  import { filebinUpload, newBin } from '../lib/filebin.ts';
  import { storageByName, storages } from '../lib/storage.ts';
  import { deleteLink, filebinLink, shareLink } from '../lib/route.ts';
  import { formatBytes, formatDate } from '../lib/format.ts';
  import CopyField from '../components/CopyField.svelte';
  import ErrorMessage from '../components/ErrorMessage.svelte';
  import KeyPicker from '../components/KeyPicker.svelte';
  import MailLink from '../components/MailLink.svelte';
  import Info from '../components/Info.svelte';
  import SecretField from '../components/SecretField.svelte';
  import StorageStatus from '../components/StorageStatus.svelte';
  import Step from '../components/Step.svelte';
  import CameraCapture, { type Photo } from '../components/CameraCapture.svelte';
  import { photoName, photoSummary } from '../lib/camera.ts';
  import { tick } from 'svelte';

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

  let what = $state<'text' | 'file' | 'photo'>('text');
  let text = $state('');
  let file = $state<File | null>(null);
  // Raw: a Blob must not be wrapped in a proxy. Only in memory, see
  // CameraCapture.
  let photo = $state.raw<Photo | null>(null);
  $effect(() => {
    if (what !== 'photo') photo = null;
  });

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

  const total = $derived(
    what === 'file' ? (file?.size ?? 0) : what === 'photo' ? (photo?.blob.size ?? 0) : new TextEncoder().encode(text).length,
  );
  const contentReady = $derived(
    what === 'text' ? text.length > 0 : what === 'photo' ? photo !== null : file !== null && file.size <= maxFileSize,
  );
  const recipientsReady = $derived(
    mode === 'password' ? password.length > 0 : recipients.length > 0 && recipients.every((r) => r.info.canEncrypt),
  );

  function pickFile(e: Event & { currentTarget: HTMLInputElement }) {
    file = e.currentTarget.files?.[0] ?? null;
    error = file && file.size > maxFileSize ? `This file is ${formatBytes(file.size)}; the limit is 100 MB.` : '';
  }

  // The button stays enabled and says what is missing, with the focus on
  // the field to fix.
  function missing(): { step: number; message: string; focus: string } | null {
    if (!contentReady) {
      if (what === 'text') return { step: 1, message: 'Type a message first.', focus: '#send-content' };
      if (what === 'photo') return { step: 1, message: 'Take a photo first.', focus: '#send-content' };
      if (!file) return { step: 1, message: 'Choose a file first.', focus: '#send-content' };
      return { step: 1, message: `This file is ${formatBytes(file.size)}; the limit is 100 MB.`, focus: '#send-content' };
    }
    if (!recipientsReady) {
      if (mode === 'password') return { step: 2, message: 'Choose a password first, or generate one.', focus: '#send-password' };
      if (recipients.length === 0) return { step: 2, message: "Add the recipient's public key first.", focus: '[role="tab"][aria-selected="true"]' };
      const bad = recipients.find((r) => !r.info.canEncrypt);
      return { step: 2, message: `Remove the key that cannot be used: ${unusableText(bad?.info.unusable)}.`, focus: '.recipients button' };
    }
    return null;
  }

  let tried = $state(false);
  const gap = $derived(tried ? missing() : null);

  // Step by step: one step open, earlier ones as a summary, later ones not
  // shown. Step 3 has defaults and the submit button, so everyone sees how
  // long the file stays and how often it can be downloaded.
  const steps = 3;
  let openStep = $state(1);

  const contentSummary = $derived(
    what === 'text'
      ? text
        ? `Text, ${text.length} ${text.length === 1 ? 'character' : 'characters'}`
        : 'No message yet'
      : what === 'photo'
        ? photo
          ? photoSummary(photo.width, photo.height, formatBytes(photo.blob.size))
          : 'No photo yet'
        : file
        ? `${file.name}, ${formatBytes(file.size)}`
        : 'No file yet',
  );
  const recipientsSummary = $derived(
    mode === 'password'
      ? password
        ? 'Anyone with the password'
        : 'No password yet'
      : recipients.length
        ? `Holders of the key of ${recipients.map((r) => r.info.userIDs[0] ?? r.info.fingerprint).join(', ')}`
        : 'No key yet',
  );
  const storageSummary = $derived(
    direct
      ? `${chosen.short}, directly from your browser; kept about ${chosen.keepsDays} days, no download limit`
      : `${chosen.name === 'local' ? 'FileBunny' : `${chosen.short}, through FileBunny`}; expires after ${
          expiries.find((o) => o.value === expires)?.label ?? expires
        }, ${maxDownloads === 0 ? 'unlimited downloads' : maxDownloads === 1 ? 'one download' : `${maxDownloads} downloads`}`,
  );

  /** Opens a step and moves the focus into it. */
  async function show(step: number, focus?: string) {
    openStep = step;
    await tick();
    // The first field to type in or choose from; not the choice buttons
    // above it, which already have a sensible value.
    const field = 'input:not([type=radio]):not([type=checkbox]), textarea, select';
    const target = focus ?? field.split(', ').map((f) => `#step-${step} ${f}`).join(', ');
    document.querySelector<HTMLElement>(target)?.focus();
  }

  /** Next: check this step, then open the next one. */
  async function goOn(step: number) {
    const todo = missing();
    if (todo && todo.step === step) {
      tried = true;
      document.querySelector<HTMLElement>(todo.focus)?.focus();
      return;
    }
    tried = false;
    await show(step + 1);
  }

  async function back(step: number) {
    tried = false;
    await show(step - 1);
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    // Enter in a field of an earlier step means Next, not upload.
    if (openStep < steps) {
      await goOn(openStep);
      return;
    }
    const todo = missing();
    if (todo) {
      tried = true;
      await show(todo.step, todo.focus);
      return;
    }
    busy = true;
    error = '';
    sent = 0;
    controller = new AbortController();
    try {
      const plaintext =
        what === 'text'
          ? { kind: 'text' as const, text }
          : what === 'photo'
            ? { kind: 'file' as const, name: photoName, data: photo!.blob.stream() }
            : { kind: 'file' as const, name: file!.name, data: file!.stream() };
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
      // Sent: the photo is not needed in this page any more.
      photo = null;
    } catch (err) {
      if (controller.signal.aborted) error = 'Upload canceled.';
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
    photo = null;
    password = '';
    sent = 0;
    openStep = 1;
  }
</script>

{#if directBin}
  <section class="card done">
    <h1>Encrypted and uploaded to {chosen.short}</h1>
    <CopyField primary label="Share link" value={filebinLink(siteBase(), directBin)} />
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
  <section class="card done">
    <h1>Encrypted and uploaded</h1>
    <CopyField primary label="Share link" value={shareLink(siteBase(), result.id)} />
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
    <MailLink id={result.id} deleteToken={result.deleteToken} />
    <CopyField label="Delete link (keep this to yourself)" value={deleteLink(siteBase(), result.id, result.deleteToken)} />
    <button type="button" onclick={startOver}>Share something else</button>
  </section>
{:else}
  <h1 class="visually-hidden">Share a file or message</h1>
  <form onsubmit={submit}>
    <Step n={1} total={steps} error={gap?.step === 1 ? gap.message : ''} current={openStep} title="What do you want to share?" summary={contentSummary}
      disabled={busy} onchange={() => show(1)} onback={() => back(1)} onnext={() => goOn(1)}>
      <div class="choice">
        <label><input type="radio" bind:group={what} value="text" /> Text</label>
        <label><input type="radio" bind:group={what} value="file" /> File</label>
        <label><input type="radio" bind:group={what} value="photo" /> Photo</label>
      </div>
      {#if what === 'text'}
        <label class="field">
          <span>Message</span>
          <textarea id="send-content" bind:value={text} rows="8" spellcheck="false"></textarea>
        </label>
      {:else if what === 'photo'}
        <CameraCapture id="send-content" bind:photo />
      {:else}
        <label class="field">
          <span>File (max 100 MB)</span>
          <input id="send-content" type="file" onchange={pickFile} />
        </label>
        {#if file}<p class="note">{file.name}, {formatBytes(file.size)}</p>{/if}
      {/if}
    </Step>

    <Step n={2} total={steps} error={gap?.step === 2 ? gap.message : ''} current={openStep} title="How do you want to encrypt it?" summary={recipientsSummary}
      disabled={busy} onchange={() => show(2)} onback={() => back(2)} onnext={() => goOn(2)}>
      <div class="choice tiles">
        <label><input type="radio" bind:group={mode} value="password" />
          <span><b><span aria-hidden="true">🔑</span> Password</b><small>Anyone with the password</small></span></label>
        <label><input type="radio" bind:group={mode} value="key" />
          <span><b><span aria-hidden="true">👤</span> Public key</b><small>Only the holders of the key</small></span></label>
      </div>
      {#if mode === 'password'}
        <SecretField
          id="send-password"
          label="Password"
          bind:value={password}
          shortWarning="Short passwords can be guessed: anyone with the link can try as many as they like, offline. Use Generate, or a long passphrase."
        />
      {:else}
        <KeyPicker bind:recipients />
      {/if}
    </Step>

    <Step n={3} total={steps} error={gap?.step === 3 ? gap.message : ''} current={openStep} title="Where, how long and how often?" summary={storageSummary}
      disabled={busy} submitLabel="Encrypt and upload" onchange={() => show(3)} onback={() => back(3)} onnext={() => goOn(3)}>
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
            <strong>Through FileBunny:</strong> this server passes the file on, so {chosen.short} sees this server
            instead of you, and the limits below apply. <strong>Directly:</strong> your browser uploads to
            {chosen.short} itself, so {chosen.short} sees your IP address and FileBunny does not know about the file.
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
    </Step>

    <ErrorMessage {error} />
    {#if busy && direct}
      <progress></progress>
      <button type="button" onclick={() => controller?.abort()}>Cancel</button>
    {:else if busy}
      <!-- Until the first bytes go out: encrypting and the proof of work,
           which takes a few seconds on older phones. -->
      {#if sent === 0}<p class="note" role="status">Encrypting and preparing the upload…</p>{/if}
      <progress max={total} value={Math.min(sent, total)}></progress>
      <button type="button" onclick={() => controller?.abort()}>Cancel</button>
    {/if}
  </form>
{/if}
