// Finding a recipient's public key by e-mail address, and checking a key
// against the recipient's own domain. See docs/architecture.md.
//
// - keys.openpgp.org is asked directly from the browser: the keyserver
//   sees the visitor's IP address and the address looked up; FileBunny
//   sees nothing. It is the only host besides the API in connect-src.
// - WKD (the recipient's domain) goes through our backend, because those
//   domains cannot be listed in the CSP. Off unless the user asks for it.
//
// Everything that comes back is untrusted: keys are parsed here, filtered
// on the address and on whether they can encrypt, and never selected
// automatically.
import type { PublicKey } from 'openpgp';
import { apiBase } from './api.ts';
import { readPublicKeys, type KeyInfo } from './crypto.ts';

export type Source = 'keys.openpgp.org' | 'wkd';

/** How much each source vouches for the key. */
export const sourceLabels: Record<Source, { name: string; trust: string }> = {
  'keys.openpgp.org': { name: 'keys.openpgp.org', trust: 'e-mail address verified by keys.openpgp.org' },
  wkd: { name: "recipient's domain (WKD)", trust: "published by the recipient's domain" },
};

export const lookupTimeoutMs = 10_000;
export const maxResponseBytes = 1 << 20;

/** Seams for tests; the defaults are the real thing. */
export type Deps = {
  fetch?: typeof fetch;
  apiBase?: string;
  timeoutMs?: number;
  maxBytes?: number;
};

type Resolved = Required<Deps>;

function resolve(d: Deps = {}): Resolved {
  return {
    fetch: d.fetch ?? ((input, init) => globalThis.fetch(input, init)),
    apiBase: d.apiBase ?? apiBase,
    timeoutMs: d.timeoutMs ?? lookupTimeoutMs,
    maxBytes: d.maxBytes ?? maxResponseBytes,
  };
}

/** A key a source returned but that is not offered, and why. */
export type Dropped = { fingerprint: string; reason: string };

export type SourceResult = {
  source: Source;
  status: 'found' | 'not-found' | 'failed';
  /** Usable keys: a valid user ID with the address, able to encrypt. */
  keys: KeyInfo[];
  dropped: Dropped[];
  /** Why the lookup failed. */
  message?: string;
};

/** A usable key and every source that returned it. */
export type Candidate = { info: KeyInfo; sources: Source[] };

export type LookupResult = {
  email: string;
  results: SourceResult[];
  candidates: Candidate[];
  /** More than one key: sources disagree, or one returned several. */
  conflict: boolean;
};

/** Loose check before asking anyone; the sources decide the rest. */
export function looksLikeEmail(s: string): boolean {
  return /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(s);
}

/**
 * Looks the address up on keys.openpgp.org and, if wkd is set, via the
 * backend's WKD proxy, in parallel. Never throws for a failing source.
 */
export async function lookupKeys(email: string, opts: { wkd: boolean }, deps?: Deps): Promise<LookupResult> {
  const d = resolve(deps);
  email = email.trim();
  if (!looksLikeEmail(email)) throw new Error('Enter an e-mail address.');
  const sources: Source[] = opts.wkd ? ['keys.openpgp.org', 'wkd'] : ['keys.openpgp.org'];
  const results = await Promise.all(sources.map((s) => lookupSource(s, email, d)));

  const byFingerprint = new Map<string, Candidate>();
  for (const r of results) {
    for (const info of r.keys) {
      const c = byFingerprint.get(info.fingerprint);
      if (c) c.sources.push(r.source);
      else byFingerprint.set(info.fingerprint, { info, sources: [r.source] });
    }
  }
  const candidates = [...byFingerprint.values()];
  return { email, results, candidates, conflict: candidates.length > 1 };
}

async function lookupSource(source: Source, email: string, d: Resolved): Promise<SourceResult> {
  const result: SourceResult = { source, status: 'not-found', keys: [], dropped: [] };
  let body: Uint8Array | null;
  try {
    body = source === 'wkd' ? await fetchWkd(email, d) : await fetchKeysOpenpgpOrg(email, d);
  } catch (err) {
    return { ...result, status: 'failed', message: errorMessage(err) };
  }
  if (body === null) return result;

  let infos: KeyInfo[];
  try {
    infos = await readPublicKeys(body);
  } catch {
    return { ...result, status: 'failed', message: 'the response is not a valid OpenPGP public key' };
  }
  const { usable, dropped } = await filterKeys(email, infos);
  return { ...result, status: usable.length > 0 ? 'found' : 'not-found', keys: usable, dropped };
}

/** Keeps keys with a valid user ID for exactly this address that can encrypt. */
export async function filterKeys(email: string, infos: KeyInfo[]): Promise<{ usable: KeyInfo[]; dropped: Dropped[] }> {
  const want = email.trim().toLowerCase();
  const usable: KeyInfo[] = [];
  const dropped: Dropped[] = [];
  for (const info of infos) {
    if (!(await keyEmails(info.key)).includes(want)) {
      dropped.push({ fingerprint: info.fingerprint, reason: 'no valid user ID with this address' });
    } else if (!info.canEncrypt) {
      dropped.push({ fingerprint: info.fingerprint, reason: 'cannot encrypt (expired, revoked or no encryption subkey)' });
    } else {
      usable.push(info);
    }
  }
  return { usable, dropped };
}

/** The lowercased e-mail addresses of the key's valid (not revoked) user IDs. */
export async function keyEmails(key: PublicKey): Promise<string[]> {
  const out = new Set<string>();
  for (const user of key.users) {
    const uid = user.userID;
    if (!uid) continue;
    const email = uid.email || (looksLikeEmail(uid.userID.trim()) ? uid.userID.trim() : '');
    if (!email) continue;
    try {
      await user.verify();
      out.add(email.toLowerCase());
    } catch {
      // Revoked, expired or without a valid self-signature.
    }
  }
  return [...out];
}

export type WkdCheck = {
  email: string;
  status: 'match' | 'different' | 'none' | 'failed';
  message?: string;
};

/**
 * Optional correctness check of a key the user already has: asks the
 * domain of every address on the key (via the backend) which key it
 * publishes, and compares fingerprints. "none" is neutral: most domains
 * publish no keys.
 */
export async function checkKeyWithWkd(info: KeyInfo, deps?: Deps): Promise<WkdCheck[]> {
  const d = resolve(deps);
  const emails = await keyEmails(info.key);
  return Promise.all(
    emails.map(async (email): Promise<WkdCheck> => {
      let body: Uint8Array | null;
      try {
        body = await fetchWkd(email, d);
      } catch (err) {
        return { email, status: 'failed', message: errorMessage(err) };
      }
      if (body === null) return { email, status: 'none' };
      let published: KeyInfo[];
      try {
        published = await readPublicKeys(body);
      } catch {
        return { email, status: 'failed', message: 'the domain published something that is not a valid public key' };
      }
      return published.some((p) => p.fingerprint === info.fingerprint)
        ? { email, status: 'match' }
        : { email, status: 'different' };
    }),
  );
}

/** What keys.openpgp.org says about each address of a published key. */
export type PublishStatus = { email: string; status: 'published' | 'pending' | 'unpublished' | 'revoked' };

/**
 * Uploads a public key to keys.openpgp.org (VKS API), directly from the
 * browser, and asks it to send a verification mail to every address that
 * is not published yet. keys.openpgp.org publishes an address only after
 * its owner clicks the link in that mail; until then only the key
 * material (without user IDs) is findable by fingerprint.
 */
export async function publishKey(publicKey: string, deps?: Deps): Promise<PublishStatus[]> {
  const d = resolve(deps);
  const post = async (path: string, body: unknown) => {
    const res = await fetchCapped(
      `https://keys.openpgp.org/vks/v1/${path}`,
      { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) },
      d,
    );
    if (res.status !== 200) throw new LookupError(`keys.openpgp.org refused the key (status ${res.status})`);
    const json = JSON.parse(new TextDecoder().decode(res.body)) as { token?: unknown; status?: unknown };
    if (typeof json.token !== 'string' || typeof json.status !== 'object' || json.status === null) {
      throw new LookupError('unexpected answer from keys.openpgp.org');
    }
    return { token: json.token, status: json.status as Record<string, string> };
  };
  try {
    const up = await post('upload', { keytext: publicKey });
    const unverified = Object.entries(up.status)
      .filter(([, st]) => st === 'unpublished')
      .map(([email]) => email);
    const final = unverified.length > 0 ? await post('request-verify', { token: up.token, addresses: unverified }) : up;
    return Object.entries(final.status).map(([email, status]) => ({ email, status: status as PublishStatus['status'] }));
  } catch (err) {
    throw new Error(err instanceof LookupError ? err.message : 'keys.openpgp.org could not be reached');
  }
}

/** Whether the backend offers the WKD proxy. False when unreachable. */
export async function wkdAvailable(deps?: Deps): Promise<boolean> {
  const d = resolve(deps);
  try {
    const res = await fetchCapped(`${d.apiBase}/api/v1/features`, { method: 'GET' }, d);
    if (res.status !== 200) return false;
    return (JSON.parse(new TextDecoder().decode(res.body)) as { wkd?: unknown }).wkd === true;
  } catch {
    return false;
  }
}

/** The key bytes, or null when there is none. */
async function fetchKeysOpenpgpOrg(email: string, d: Resolved): Promise<Uint8Array | null> {
  const res = await fetchCapped(`https://keys.openpgp.org/vks/v1/by-email/${encodeURIComponent(email)}`, { method: 'GET' }, d);
  if (res.status === 404) return null;
  if (res.status !== 200) throw new LookupError(`unexpected response (status ${res.status})`);
  return res.body;
}

/** The key bytes, or null when the domain publishes none. */
async function fetchWkd(email: string, d: Resolved): Promise<Uint8Array | null> {
  const res = await fetchCapped(
    `${d.apiBase}/api/v1/wkd`,
    { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email }) },
    d,
  );
  switch (res.status) {
    case 200:
      return res.body.length > 0 ? res.body : null;
    case 404:
      return null;
    case 400:
      throw new LookupError('this address cannot be looked up with WKD');
    case 503:
      throw new LookupError('the server is busy, try again in a moment');
    default:
      throw new LookupError("the recipient's domain could not be reached");
  }
}

class LookupError extends Error {}

function errorMessage(err: unknown): string {
  return err instanceof LookupError ? err.message : 'not reachable';
}

/**
 * fetch with a timeout and a size cap, no cookies, no referrer, no cache,
 * no redirects. Only a 200 body is read; it is read as a stream so a huge
 * response is cut off instead of buffered.
 */
async function fetchCapped(url: string, init: RequestInit, d: Resolved): Promise<{ status: number; body: Uint8Array }> {
  const ctrl = new AbortController();
  const timeout = new LookupError('timed out');
  const timer = setTimeout(() => ctrl.abort(timeout), d.timeoutMs);
  try {
    const res = await d.fetch(url, {
      ...init,
      signal: ctrl.signal,
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
      cache: 'no-store',
      redirect: 'error',
    });
    if (res.status !== 200) {
      void res.body?.cancel().catch(() => {});
      return { status: res.status, body: new Uint8Array() };
    }
    return { status: 200, body: await readCapped(res, d.maxBytes, ctrl) };
  } catch (err) {
    if (ctrl.signal.aborted && ctrl.signal.reason instanceof LookupError) throw ctrl.signal.reason;
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

async function readCapped(res: Response, max: number, ctrl: AbortController): Promise<Uint8Array> {
  const tooLarge = new LookupError('the response is too large');
  if (Number(res.headers.get('Content-Length') ?? 0) > max) {
    ctrl.abort(tooLarge);
    throw tooLarge;
  }
  if (!res.body) {
    const all = new Uint8Array(await res.arrayBuffer());
    if (all.length > max) throw tooLarge;
    return all;
  }
  const reader = res.body.getReader();
  const parts: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    size += value.length;
    if (size > max) {
      ctrl.abort(tooLarge);
      void reader.cancel().catch(() => {});
      throw tooLarge;
    }
    parts.push(value);
  }
  const out = new Uint8Array(size);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

/** A key on the send page's recipient list. */
export type Recipient = {
  info: KeyInfo;
  /** Where the key came from, for display. */
  source: string;
  /** Result of the optional WKD check, once asked for. */
  check?: WkdCheck[] | 'busy';
};
