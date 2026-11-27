// Filebin (filebin.net) straight from the browser: the direct route of
// docs/storage/filebin.md. FileBunny's server is not involved and knows
// nothing about the file; Filebin sees the visitor's IP address.
//
// Each file gets its own bin with a random name, made here; the file in it
// is always data.bin, so the real file name never reaches Filebin. The bin
// name travels only in the link's fragment.
//
// Filebin lets anyone who knows a bin's name upload to it, so whoever sees
// the link could replace data.bin. Locking the bin needs a PUT, which
// Filebin's CORS answer does not allow. So the link also carries the
// SHA-256 of the ciphertext, and nothing is decrypted that does not match.
import { maxCiphertext, readBody } from './api.ts';

/** Keep in sync with connect-src in deploy/Caddyfile and vite.config.ts. */
export const filebinOrigin = 'https://filebin.net';

/** 128 random bits as lowercase hex, as the server names its bins. */
export const binPattern = /^[0-9a-f]{32}$/;

/** SHA-256 of the ciphertext as lowercase hex, as it goes into the link. */
export const sumPattern = /^[0-9a-f]{64}$/;

/** About how long Filebin keeps a bin, for the page texts. */
export const filebinKeepsDays = 7;

export function newBin(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** Where the file is at Filebin; also what the recipient opens in a tab. */
export function filebinFileUrl(bin: string): string {
  if (!binPattern.test(bin)) throw new Error('invalid bin name');
  return `${filebinOrigin}/${bin}/data.bin`;
}

export async function sha256Hex(data: Blob | Uint8Array): Promise<string> {
  // Never a SharedArrayBuffer here (no cross-origin isolation); no copy of
  // up to 100 MiB just for the type.
  const bytes = data instanceof Blob ? await data.arrayBuffer() : (data as Uint8Array<ArrayBuffer>);
  const sum = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
  return Array.from(sum, (x) => x.toString(16).padStart(2, '0')).join('');
}

/** Whether data is the file the link was made for. */
export async function matchesSum(data: Uint8Array, sum: string): Promise<boolean> {
  return sumPattern.test(sum) && (await sha256Hex(data)) === sum;
}

/** Seams for tests; the default is the real thing. */
export type Deps = { fetch?: typeof fetch };

const init = {
  // No cookies and no referrer: Filebin learns nothing about FileBunny's
  // page, not even which link was opened.
  credentials: 'omit',
  referrerPolicy: 'no-referrer',
  cache: 'no-store',
} as const;

/**
 * Uploads ciphertext as a CORS simple request: a POST without extra
 * headers. The Blob has no type, so no Content-Type is sent either.
 * Returns the SHA-256 of what was uploaded, for the link.
 */
export async function filebinUpload(bin: string, ciphertext: Blob, signal?: AbortSignal, deps: Deps = {}): Promise<string> {
  if (ciphertext.size > maxCiphertext) throw new RangeError('too large');
  const sum = await sha256Hex(ciphertext);
  const res = await (deps.fetch ?? fetch)(filebinFileUrl(bin), {
    ...init,
    method: 'POST',
    body: new Blob([ciphertext]),
    signal,
  });
  if (res.status !== 201) throw new Error(`Filebin answered ${res.status}`);
  return sum;
}

export type Fetched =
  | { kind: 'data'; data: Uint8Array }
  /** Filebin wants to check the browser first: open the file in a tab. */
  | { kind: 'tab' }
  | { kind: 'gone' };

/**
 * Tries to download the ciphertext. Filebin may answer with a verification
 * page instead, which only works in a tab of its own; that, and a request
 * the browser blocks, both mean: open it in a tab.
 */
export async function filebinFetch(
  bin: string,
  onProgress: (received: number) => void = () => {},
  signal?: AbortSignal,
  deps: Deps = {},
): Promise<Fetched> {
  let res: Response;
  try {
    res = await (deps.fetch ?? fetch)(filebinFileUrl(bin), { ...init, signal });
  } catch (err) {
    if (signal?.aborted) throw err;
    return { kind: 'tab' };
  }
  if (res.status === 404) return { kind: 'gone' };
  const type = res.headers.get('Content-Type') ?? '';
  if (!res.ok || type.startsWith('text/html')) {
    void res.body?.cancel();
    return { kind: 'tab' };
  }
  return { kind: 'data', data: await readBody(res, onProgress, maxCiphertext) };
}
