<script lang="ts">
  // Recipients of a public-key message. On top, a frame with the chosen
  // keys; below it, two tabs to add one: from this device (file,
  // drag-and-drop, paste) or by e-mail on keys.openpgp.org, optionally also
  // from the recipient's domain (WKD, via our server). Once there is a
  // recipient the tabs fold away. User IDs come from keys and are
  // untrusted: they are only ever rendered as text.
  import { onMount, tick } from 'svelte';
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
  import Info from './Info.svelte';

  let { recipients = $bindable() }: { recipients: Recipient[] } = $props();

  /** Whether this server offers the WKD proxy; hidden otherwise. */
  let wkdOffered = $state(false);
  const server = new URL(siteBase()).host;
  onMount(async () => {
    wkdOffered = await wkdAvailable();
  });

  /**
   * What a new key does: with no recipient, or after "Change key", it
   * replaces the recipients; after "Add another recipient" it is added.
   * 'closed' folds the tabs away once there is a recipient.
   */
  let mode = $state<'closed' | 'change' | 'another'>('closed');
  const adding = $derived(recipients.length === 0 || mode !== 'closed');
  let tab = $state<'device' | 'lookup'>('device');
  let replacedNote = $state('');

  // From this device.
  let keyInput = $state('');
  let localError = $state('');
  let dragging = $state(false);
  let pasted = false;

  // By e-mail address.
  let email = $state('');
  let useWkd = $state(false);
  let searching = $state(false);
  let lookupError = $state('');
  let lookup = $state.raw<LookupResult | null>(null);

  let copied = $state('');

  const added = (fingerprint: string) => recipients.some((r) => r.info.fingerprint === fingerprint);

  /**
   * Makes the keys of one action (a file, a paste, "Use this key") the
   * recipients. They replace the current ones, so a key added by mistake
   * does not silently stay on the list; after "Add another recipient" they
   * are added instead.
   */
  function addRecipients(infos: KeyInfo[], source: string) {
    if (infos.length === 0) return;
    const fresh = infos.filter((i) => !added(i.fingerprint)).map((info) => ({ info, source }));
    if (mode === 'another') {
      recipients = [...recipients, ...fresh];
      replacedNote = '';
    } else {
      const kept = recipients.filter((r) => infos.some((i) => i.fingerprint === r.info.fingerprint));
      const gone = recipients.filter((r) => !kept.includes(r));
      replacedNote =
        gone.length > 0 ? `Replaced ${gone.map((r) => r.info.userIDs[0] ?? r.info.fingerprint).join(', ')}.` : '';
      recipients = [...kept, ...fresh];
    }
    mode = 'closed';
    lookup = null;
    email = '';
  }

  function remove(fingerprint: string) {
    recipients = recipients.filter((r) => r.info.fingerprint !== fingerprint);
    replacedNote = '';
  }

  function startAdding(m: 'change' | 'another') {
    mode = m;
    replacedNote = '';
  }

  function cancelAdding() {
    mode = 'closed';
    localError = '';
    lookupError = '';
    lookup = null;
  }

  /** Reads keys from a file, a drop or the text field; returns those that can encrypt. */
  async function readLocal(input: string | Uint8Array): Promise<KeyInfo[]> {
    let infos: KeyInfo[];
    try {
      infos = await readPublicKeys(input);
    } catch (err) {
      localError =
        err instanceof Error && err.message.includes('private key') ? err.message : 'This is not a valid OpenPGP public key.';
      return [];
    }
    const usable = infos.filter((k) => k.canEncrypt);
    if (usable.length < infos.length) {
      localError = `${usable.length === 0 ? 'This key' : 'One of these keys'} cannot be used for encryption (expired, revoked or sign-only).`;
    }
    return usable;
  }

  async function addLocal(input: string | Uint8Array): Promise<boolean> {
    localError = '';
    const usable = await readLocal(input);
    addRecipients(usable, 'your device');
    return usable.length > 0;
  }

  /** Reads a pasted key as soon as it is complete; no button needed. */
  async function keyTyped() {
    const text = keyInput.trim();
    if (/-----END PGP [A-Z ]+-----/.test(text)) {
      pasted = false;
      if (await addLocal(text)) keyInput = '';
    } else if (pasted) {
      pasted = false;
      localError = 'Paste the whole key, from -----BEGIN PGP PUBLIC KEY BLOCK----- to -----END PGP PUBLIC KEY BLOCK-----.';
    } else if (text === '') {
      localError = '';
    }
  }

  /** All files of one pick or drop count as one action. */
  async function addFiles(files: FileList | null | undefined) {
    localError = '';
    const all: KeyInfo[] = [];
    for (const f of files ?? []) {
      all.push(...(await readLocal(new Uint8Array(await f.arrayBuffer()))));
      if (localError) return;
    }
    addRecipients(all, 'your device');
  }

  async function pickKeyFile(e: Event & { currentTarget: HTMLInputElement }) {
    // currentTarget is null once the event has been dispatched, so keep it.
    const input = e.currentTarget;
    await addFiles(input.files);
    // Lets the same file be chosen again after removing its key.
    input.value = '';
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
    addRecipients([c.info], c.sources.map((s) => sourceLabels[s].name).join(' + '));
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

  // Tabs: arrow keys move between them, as for any tab list.
  const tabs = ['device', 'lookup'] as const;
  async function tabKey(e: KeyboardEvent) {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    e.preventDefault();
    tab = tab === 'device' ? 'lookup' : 'device';
    await tick();
    document.getElementById(`keytab-${tab}`)?.focus();
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

  const statusClass = { found: 'ok', 'not-found': 'notice', failed: 'error' } as const;

  /** Per-source details only when something needs attention. */
  const showSources = $derived(
    lookup !== null &&
      (lookup.candidates.length === 0 || lookup.conflict || lookup.results.some((r) => r.status === 'failed')),
  );

  function checkText(c: WkdCheck, many: boolean): string {
    const who = many ? `${c.email}: ` : '';
    switch (c.status) {
      case 'match':
        return `✓ ${who}the recipient's domain publishes this key`;
      case 'different':
        return `⚠ ${who}the recipient's domain publishes a different key. Do not use this key unless the recipient confirms its fingerprint.`;
      case 'none':
        return `${who}the domain publishes no key (common, not a problem)`;
      case 'failed':
        return `${who}domain check failed (${c.message})`;
    }
  }
</script>

{#snippet sourceList(l: LookupResult)}
  <ul class="sources">
    {#each l.results as r (r.source)}
      <li class={statusClass[r.status]}>
        {sourceLabels[r.source].name}: {sourceStatus(r)}
        {#each r.dropped as d (d.fingerprint)}
          <div class="note">Ignored <code>{d.fingerprint}</code>: {d.reason}</div>
        {/each}
      </li>
    {/each}
  </ul>
{/snippet}

<section class="recipients" aria-labelledby="recipients-title">
  <h3 id="recipients-title">{recipients.length > 1 ? `Recipients (${recipients.length})` : 'Recipient'}</h3>
  {#if recipients.length === 0}
    <p class="note">No recipient yet. Add the recipient's public key below.</p>
  {/if}
  {#if recipients.length > 1}
    <p class="multi" role="status">
      One message for all of them: each recipient can open it with their own private key. Remove any key you added by
      mistake.
    </p>
  {/if}
  {#if replacedNote}<p class="note">{replacedNote}</p>{/if}

  {#each recipients as r (r.info.fingerprint)}
    {@const many = r.check !== undefined && r.check !== 'busy' && r.check.length > 1}
    <div class="key chosen">
      {#each r.info.userIDs as uid (uid)}<div class="uid">{uid}</div>{/each}
      <code class="fingerprint">{r.info.fingerprint}</code>
      <div class="row actions">
        <span class="note">from {r.source}</span>
        <button type="button" class="small" onclick={() => copy(r.info.fingerprint)}>
          {copied === r.info.fingerprint ? 'Copied' : 'Copy fingerprint'}
        </button>
        {#if wkdOffered && r.check === undefined}
          <button type="button" class="small" onclick={() => check(r.info.fingerprint)}>Check with domain (WKD)</button>
        {/if}
        <button type="button" class="small" onclick={() => remove(r.info.fingerprint)}>Remove</button>
      </div>
      {#if r.check === 'busy'}
        <div class="note">Checking with the recipient's domain…</div>
      {:else if r.check !== undefined && r.check.length === 0}
        <div class="note">This key has no e-mail address to check.</div>
      {:else if r.check !== undefined}
        {#each r.check as c (c.email)}
          <div class={c.status === 'different' ? 'warning' : c.status === 'match' ? 'ok' : 'note'}>{checkText(c, many)}</div>
        {/each}
      {/if}
    </div>
  {/each}

  {#if recipients.length > 0}
    {#if !adding}
      <div class="row">
        {#if recipients.length === 1}
          <button type="button" onclick={() => startAdding('change')}>Change key</button>
        {/if}
        <button type="button" onclick={() => startAdding('another')}>+ Add another recipient</button>
      </div>
    {/if}
    <p class="note">
      Compare the fingerprint with the recipient before you send anything.
      <Info label="Why compare the fingerprint?">
        A keyserver or a domain can hand out a key that is not the recipient's. Only the fingerprint, checked with the
        recipient in person or by phone, proves the key is theirs.
        {#if wkdOffered}The domain check is optional: it asks the recipient's domain through {server}, which then sees
          the address (not logged or stored).{/if}
      </Info>
    </p>
  {/if}
</section>

{#if adding}
  <div class="adder">
    {#if mode !== 'closed' && recipients.length > 0}
      <p class="multi" role="status">
        {mode === 'another'
          ? 'Adding another recipient: the key you choose is added to the list.'
          : 'Changing the key: the key you choose replaces the current one.'}
        <button type="button" class="small" onclick={cancelAdding}>Cancel</button>
      </p>
    {/if}

    <div class="tabs" role="tablist" aria-label="How to add a public key">
      {#each tabs as t (t)}
        <button
          type="button"
          role="tab"
          id={`keytab-${t}`}
          aria-selected={tab === t}
          aria-controls={`keypanel-${t}`}
          tabindex={tab === t ? 0 : -1}
          onclick={() => (tab = t)}
          onkeydown={tabKey}
        >
          {t === 'device' ? 'From your device' : 'Look up by e-mail'}
        </button>
      {/each}
    </div>

    {#if tab === 'device'}
      <div class="tabpanel" role="tabpanel" id="keypanel-device" aria-labelledby="keytab-device">
        <div
          class="dropzone"
          class:dragging
          role="group"
          aria-label="Drop a key file"
          ondragover={dragOver}
          ondragleave={() => (dragging = false)}
          ondrop={drop}
        >
          <label class="field">
            <span>Drop a key file here, or paste a public key</span>
            <textarea
              bind:value={keyInput}
              onpaste={() => (pasted = true)}
              oninput={keyTyped}
              rows="3"
              spellcheck="false"
              placeholder="-----BEGIN PGP PUBLIC KEY BLOCK-----"
            ></textarea>
          </label>
          <label class="button filebutton">
            Choose file…
            <input
              type="file"
              class="visually-hidden"
              multiple
              accept=".asc,.gpg,.pgp,.key,application/pgp-keys"
              onchange={pickKeyFile}
            />
          </label>
        </div>
        <p class="note">Read in your browser only, never uploaded.</p>
        <ErrorMessage error={localError} />
      </div>
    {:else}
      <div class="tabpanel" role="tabpanel" id="keypanel-lookup" aria-labelledby="keytab-lookup">
        <label class="field">
          <span>Recipient's e-mail address</span>
          <span class="row">
            <input type="email" bind:value={email} onkeydown={searchOnEnter} autocomplete="off" spellcheck="false" />
            <button type="button" onclick={search} disabled={searching}>{searching ? 'Looking up…' : 'Look up key'}</button>
          </span>
        </label>
        <p class="note">
          keys.openpgp.org sees your IP address and the address you look up.
          <Info label="About the keyserver lookup">
            Your browser asks keys.openpgp.org directly, so {server} does not see the lookup. keys.openpgp.org only
            publishes addresses it has verified by e-mail; that shows the address is real, not who holds the key.
          </Info>
        </p>
        {#if wkdOffered}
          <p class="checkbox">
            <label><input type="checkbox" bind:checked={useWkd} /> Also ask the recipient's domain (WKD)</label>
            <span class="note">– {server} then sees the address.</span>
            <Info label="About WKD">
              With WKD the key comes from the recipient's own domain, for example example.org for bob@example.org. Your
              browser cannot reach every domain, so {server} fetches it and sees the address; it does not log or store
              it. Most domains publish no keys.
            </Info>
          </p>
        {/if}
        <ErrorMessage error={lookupError} />

        {#if lookup}
          {#if showSources}
            {@render sourceList(lookup)}
          {/if}
          {#if lookup.conflict}
            <p class="warning" role="alert">
              Different keys were found for {lookup.email}. At most one of them belongs to the recipient. Ask the
              recipient for their fingerprint and use only the key that matches.
            </p>
          {/if}
          {#each lookup.candidates as c (c.info.fingerprint)}
            <div class="key found">
              {#each c.info.userIDs as uid (uid)}<div class="uid">{uid}</div>{/each}
              <code class="fingerprint">{c.info.fingerprint}</code>
              <div class="row actions">
                {#each c.sources as s (s)}
                  <span class="badge" title={sourceLabels[s].trust}>✓ {sourceLabels[s].name}</span>
                {/each}
                <button type="button" class="primary small" onclick={() => use(c)} disabled={added(c.info.fingerprint)}>
                  {added(c.info.fingerprint) ? 'Added' : 'Use this key'}
                </button>
              </div>
            </div>
          {/each}
          {#if lookup.candidates.length === 0}
            <p class="notice">No usable key found. Ask the recipient for their public key and add it from your device.</p>
          {/if}
          {#if !showSources}
            <details class="more">
              <summary>Details per source</summary>
              {@render sourceList(lookup)}
            </details>
          {/if}
        {/if}
      </div>
    {/if}
  </div>
{/if}
