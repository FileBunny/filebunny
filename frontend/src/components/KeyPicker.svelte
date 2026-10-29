<script lang="ts">
  // Three ways to get a recipient's public key: from this device (file,
  // drag-and-drop, paste), by e-mail on keys.openpgp.org, and optionally
  // from the recipient's domain (WKD, via our server). User IDs come from
  // keys and are untrusted: they are only ever rendered as text.
  import { onMount } from 'svelte';
  import { readPublicKeys, type KeyInfo } from '../lib/crypto.ts';
  import {
    checkKeyWithWkd,
    looksLikeEmail,
    lookupKeys,
    sourceLabels,
    wkdAvailable,
    type Candidate,
    type LookupResult,
    type Recipient,
    type SourceResult,
    type WkdCheck,
  } from '../lib/keylookup.ts';
  import { siteBase } from '../lib/api.ts';
  import { copyText } from '../lib/format.ts';
  import ErrorMessage from './ErrorMessage.svelte';

  let { recipients = $bindable() }: { recipients: Recipient[] } = $props();

  /** Whether this server offers the WKD proxy; hidden otherwise. */
  let wkdOffered = $state(false);
  const server = new URL(siteBase()).host;
  onMount(async () => {
    wkdOffered = await wkdAvailable();
  });

  // From this device.
  let keyInput = $state('');
  let localError = $state('');
  let dragging = $state(false);

  // By e-mail address.
  let email = $state('');
  let useWkd = $state(false);
  let searching = $state(false);
  let lookupError = $state('');
  let lookup = $state.raw<LookupResult | null>(null);

  let copied = $state('');

  const added = (fingerprint: string) => recipients.some((r) => r.info.fingerprint === fingerprint);

  function add(info: KeyInfo, source: string) {
    if (!added(info.fingerprint)) recipients = [...recipients, { info, source }];
  }

  function remove(fingerprint: string) {
    recipients = recipients.filter((r) => r.info.fingerprint !== fingerprint);
  }

  /** Reads keys from a file, a drop or the text field; adds those that can encrypt. */
  async function addLocal(input: string | Uint8Array): Promise<boolean> {
    localError = '';
    let infos: KeyInfo[];
    try {
      infos = await readPublicKeys(input);
    } catch (err) {
      localError =
        err instanceof Error && err.message.includes('private key') ? err.message : 'This is not a valid OpenPGP public key.';
      return false;
    }
    const usable = infos.filter((k) => k.canEncrypt);
    if (usable.length < infos.length) {
      localError = `${usable.length === 0 ? 'This key' : 'One of these keys'} cannot be used for encryption (expired, revoked or sign-only).`;
    }
    for (const info of usable) add(info, 'your device');
    return usable.length > 0;
  }

  async function addPasted() {
    if (keyInput.trim() === '') return;
    if (await addLocal(keyInput.trim())) keyInput = '';
  }

  async function addFiles(files: FileList | null | undefined) {
    for (const f of files ?? []) {
      await addLocal(new Uint8Array(await f.arrayBuffer()));
      if (localError) break;
    }
  }

  async function pickKeyFile(e: Event & { currentTarget: HTMLInputElement }) {
    await addFiles(e.currentTarget.files);
    e.currentTarget.value = '';
  }

  function dragOver(e: DragEvent) {
    e.preventDefault();
    dragging = true;
  }

  async function drop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    const dt = e.dataTransfer;
    if (dt?.files.length) await addFiles(dt.files);
    else if (dt?.getData('text/plain')) await addLocal(dt.getData('text/plain').trim());
  }

  async function search() {
    lookupError = '';
    lookup = null;
    if (!looksLikeEmail(email.trim())) {
      lookupError = 'Enter the e-mail address of the recipient.';
      return;
    }
    searching = true;
    try {
      lookup = await lookupKeys(email, { wkd: wkdOffered && useWkd });
    } catch (err) {
      lookupError = err instanceof Error ? err.message : 'Lookup failed.';
    } finally {
      searching = false;
    }
  }

  function searchOnEnter(e: KeyboardEvent) {
    // Enter would otherwise submit the whole form.
    if (e.key === 'Enter') {
      e.preventDefault();
      void search();
    }
  }

  function use(c: Candidate) {
    add(c.info, c.sources.map((s) => `${sourceLabels[s].name} (${sourceLabels[s].trust})`).join(' and '));
  }

  async function check(fingerprint: string) {
    const r = recipients.find((x) => x.info.fingerprint === fingerprint);
    if (!r) return;
    setCheck(fingerprint, 'busy');
    setCheck(fingerprint, await checkKeyWithWkd(r.info));
  }

  function setCheck(fingerprint: string, check: Recipient['check']) {
    recipients = recipients.map((r) => (r.info.fingerprint === fingerprint ? { ...r, check } : r));
  }

  async function copy(fingerprint: string) {
    if (await copyText(fingerprint)) {
      copied = fingerprint;
      setTimeout(() => (copied = ''), 2000);
    }
  }

  function sourceStatus(r: SourceResult): string {
    switch (r.status) {
      case 'found':
        return r.keys.length === 1 ? 'found a key' : `found ${r.keys.length} keys`;
      case 'not-found':
        return 'no usable key';
      case 'failed':
        return `lookup failed: ${r.message}`;
    }
  }

  function checkText(c: WkdCheck): string {
    switch (c.status) {
      case 'match':
        return `${c.email}: matches, the domain publishes this key.`;
      case 'different':
        return `${c.email}: the domain publishes a different key. Do not use this key unless the recipient confirms its fingerprint.`;
      case 'none':
        return `${c.email}: the domain publishes no key. This is common and not a problem in itself.`;
      case 'failed':
        return `${c.email}: check failed (${c.message}).`;
    }
  }
</script>

<div class="keysource">
  <h3>From your device</h3>
  <p class="note">The key is read in your browser only; it is not uploaded.</p>
  <label class="field">
    <span>Paste a public key (ASCII-armored), or drop a key file here</span>
    <textarea
      class:dragging
      bind:value={keyInput}
      ondragover={dragOver}
      ondragleave={() => (dragging = false)}
      ondrop={drop}
      rows="4"
      spellcheck="false"
      placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----"
    ></textarea>
  </label>
  <div class="row">
    <button type="button" onclick={addPasted} disabled={keyInput.trim() === ''}>Add pasted key</button>
  </div>
  <label class="field">
    <span>…or choose a key file (.asc, .gpg, .pgp, .key)</span>
    <input type="file" multiple accept=".asc,.gpg,.pgp,.key,application/pgp-keys" onchange={pickKeyFile} />
  </label>
  <ErrorMessage error={localError} />
</div>

<div class="keysource">
  <h3>Look up by e-mail address</h3>
  <label class="field">
    <span>Recipient's e-mail address</span>
    <span class="row">
      <input type="email" bind:value={email} onkeydown={searchOnEnter} autocomplete="off" spellcheck="false" />
      <button type="button" onclick={search} disabled={searching}>{searching ? 'Looking up…' : 'Look up key'}</button>
    </span>
  </label>
  <p class="note">
    Your browser asks keys.openpgp.org directly. That keyserver sees your IP address and the address you look up;
    {server} does not.
  </p>
  {#if wkdOffered}
    <label class="checkbox">
      <input type="checkbox" bind:checked={useWkd} /> Also ask the recipient's domain (WKD)
    </label>
    {#if useWkd}
      <p class="note">
        This lookup goes through {server}, so this server sees which address you look up (not logged or stored).
      </p>
    {/if}
  {/if}
  <ErrorMessage error={lookupError} />

  {#if lookup}
    <ul class="sources">
      {#each lookup.results as r (r.source)}
        <li>
          {sourceLabels[r.source].name}: {sourceStatus(r)}
          {#each r.dropped as d (d.fingerprint)}
            <div class="note">Ignored <code>{d.fingerprint}</code>: {d.reason}</div>
          {/each}
        </li>
      {/each}
    </ul>
    {#if lookup.conflict}
      <p class="warning" role="alert">
        Different keys were found for {lookup.email}. At most one of them belongs to the recipient. Ask the recipient for
        their fingerprint and use only the key that matches.
      </p>
    {/if}
    {#each lookup.candidates as c (c.info.fingerprint)}
      <div class="key">
        {#each c.info.userIDs as uid (uid)}<div>{uid}</div>{/each}
        <code>{c.info.fingerprint}</code>
        {#each c.sources as s (s)}
          <div class="note">From {sourceLabels[s].name}: {sourceLabels[s].trust}</div>
        {/each}
        <button type="button" onclick={() => use(c)} disabled={added(c.info.fingerprint)}>
          {added(c.info.fingerprint) ? 'Added' : 'Use this key'}
        </button>
      </div>
    {/each}
    {#if lookup.candidates.length === 0}
      <p class="note">No usable key found. Ask the recipient for their public key and add it from your device.</p>
    {/if}
  {/if}
</div>

{#if recipients.length > 0}
  <div class="keysource">
    <h3>Recipients</h3>
    {#each recipients as r (r.info.fingerprint)}
      <div class="key">
        {#each r.info.userIDs as uid (uid)}<div>{uid}</div>{/each}
        <span class="row">
          <code>{r.info.fingerprint}</code>
          <button type="button" class="small" onclick={() => copy(r.info.fingerprint)}>
            {copied === r.info.fingerprint ? 'Copied' : 'Copy'}
          </button>
        </span>
        <div class="note">Source: {r.source}</div>
        {#if wkdOffered}
          {#if r.check === undefined}
            <button type="button" class="small" onclick={() => check(r.info.fingerprint)}>
              Check with the recipient's domain (WKD)
            </button>
          {:else if r.check === 'busy'}
            <div class="note">Checking…</div>
          {:else if r.check.length === 0}
            <div class="note">This key has no e-mail address to check.</div>
          {:else}
            {#each r.check as c (c.email)}
              <div class={c.status === 'different' ? 'warning' : c.status === 'match' ? 'ok' : 'note'}>{checkText(c)}</div>
            {/each}
          {/if}
        {/if}
        <button type="button" class="small" onclick={() => remove(r.info.fingerprint)}>Remove</button>
      </div>
    {/each}
    <p class="note">
      Compare the fingerprint with the recipient (in person or by phone) before you send anything.
      {#if wkdOffered}The WKD check is optional; it asks the domain through {server}, which then sees the address.{/if}
    </p>
  </div>
{/if}
