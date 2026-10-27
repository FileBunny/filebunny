// Client for the FileBunny API (docs/api.md). It only ever sends
// ciphertext.

/**
 * Where the API lives. Empty means the same origin as the page; the
 * downloadable single-file build sets VITE_FILEBUNNY_API to the public site,
 * because a page opened from file:// has no origin of its own.
 */
export const apiBase: string = (import.meta.env?.VITE_FILEBUNNY_API ?? '').replace(/\/+$/, '');

/** Base URL for share and delete links. */
export function siteBase(): string {
  return apiBase || location.origin;
}

/** Ciphertext is sent in parts of this size (the server allows 16 MiB). */
export const chunkSize = 8 << 20;

export class ApiError extends Error {
  readonly status: number;
  /** On a 409 for a chunk: the bytes the server has. */
  readonly offset?: number;

  constructor(status: number, message: string, offset?: number) {
    super(message);
    this.status = status;
    this.offset = offset;
  }
}

export type Limits = {
  /** Go duration, e.g. "24h". */
  expires: string;
  /** 0 means unlimited. */
  maxDownloads: number;
};

export type Uploaded = {
  id: string;
  deleteToken: string;
  size: number;
  expiresAt: Date;
  maxDownloads: number;
};

export type FileInfo = {
  size: number;
  expiresAt: Date;
  /** null when downloads are unlimited. */
  downloadsLeft: number | null;
};

type Progress = (bytesSent: number) => void;

/**
 * Uploads a ciphertext stream. Small messages go in one request; anything
 * larger than one chunk becomes a chunked upload, so the browser never
 * holds more than one chunk of ciphertext.
 */
export async function upload(
  ciphertext: ReadableStream<Uint8Array>,
  limits: Limits,
  onProgress: Progress = () => {},
  signal?: AbortSignal,
): Promise<Uploaded> {
  const query = limitsQuery(limits);
  const reader = ciphertext.getReader();
  const chunks = new Chunker(reader);
  try {
    const first = await chunks.next();
    if (first.done) {
      const res = await request('POST', `/api/v1/files?${query}`, { body: first.chunk, signal });
      onProgress(first.chunk.size);
      return toUploaded(await res.json());
    }
    return await uploadChunked(chunks, first.chunk, query, onProgress, signal);
  } finally {
    reader.releaseLock();
  }
}

async function uploadChunked(
  chunks: Chunker,
  first: Blob,
  query: string,
  onProgress: Progress,
  signal?: AbortSignal,
): Promise<Uploaded> {
  const res = await request('POST', `/api/v1/uploads?${query}`, { signal });
  const session: { id: string; upload_token: string; max_size: number } = await res.json();
  const path = `/api/v1/uploads/${encodeURIComponent(session.id)}`;
  const auth = { Authorization: `Bearer ${session.upload_token}` };
  try {
    let offset = 0;
    let next: { chunk: Blob; done: boolean } = { chunk: first, done: false };
    for (;;) {
      if (offset + next.chunk.size > session.max_size) {
        throw new ApiError(413, 'The encrypted file is larger than the server allows.');
      }
      offset = await sendChunk(path, auth, offset, next.chunk, signal);
      onProgress(offset);
      if (next.done) break;
      next = await chunks.next();
      if (next.chunk.size === 0) break;
    }
    const done = await request('POST', `${path}/complete`, { headers: auth, signal });
    return toUploaded(await done.json());
  } catch (err) {
    // Free the server's slot right away instead of waiting for the timeout.
    void request('DELETE', path, { headers: auth }).catch(() => {});
    throw err;
  }
}

/**
 * Sends one chunk, retrying network errors and server errors a few times.
 * A chunk is all or nothing on the server; after a lost response, a 409
 * tells whether it arrived.
 */
async function sendChunk(
  path: string,
  headers: Record<string, string>,
  offset: number,
  chunk: Blob,
  signal?: AbortSignal,
): Promise<number> {
  for (let attempt = 1; ; attempt++) {
    try {
      const res = await request('PUT', `${path}?offset=${offset}`, { headers, body: chunk, signal });
      return ((await res.json()) as { offset: number }).offset;
    } catch (err) {
      if (signal?.aborted) throw err;
      if (err instanceof ApiError && err.status === 409 && err.offset === offset + chunk.size) {
        return err.offset; // the previous attempt arrived after all
      }
      const retryable = !(err instanceof ApiError) || err.status >= 500 || err.status === 409;
      if (!retryable || attempt >= 4) throw err;
      await sleep(500 * 2 ** attempt, signal);
    }
  }
}

/** Cuts a stream into chunks of exactly chunkSize bytes (the last may be smaller). */
class Chunker {
  private rest: Uint8Array | null = null;
  private readonly reader: ReadableStreamDefaultReader<Uint8Array>;

  constructor(reader: ReadableStreamDefaultReader<Uint8Array>) {
    this.reader = reader;
  }

  /** The next chunk; done is true when the stream has ended. */
  async next(): Promise<{ chunk: Blob; done: boolean }> {
    const parts: Uint8Array[] = [];
    let size = 0;
    for (;;) {
      let value = this.rest;
      this.rest = null;
      if (!value) {
        const r = await this.reader.read();
        if (r.done) return { chunk: new Blob(parts as BlobPart[]), done: true };
        value = r.value;
      }
      if (size + value.length > chunkSize) {
        this.rest = value.subarray(chunkSize - size);
        value = value.subarray(0, chunkSize - size);
      }
      parts.push(value);
      size += value.length;
      if (size === chunkSize) return { chunk: new Blob(parts as BlobPart[]), done: false };
    }
  }
}

/** HEAD: expiry and downloads left, without using up a download. null if gone. */
export async function fileInfo(id: string): Promise<FileInfo | null> {
  try {
    const res = await request('HEAD', filePath(id));
    const left = res.headers.get('Downloads-Remaining');
    return {
      size: Number(res.headers.get('Content-Length')),
      expiresAt: new Date(res.headers.get('Expires-At') ?? ''),
      downloadsLeft: left === null ? null : Number(left),
    };
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

/** Downloads the ciphertext. This uses up one download. */
export async function download(id: string, onProgress: Progress = () => {}, signal?: AbortSignal): Promise<Uint8Array> {
  const res = await request('GET', filePath(id), { signal });
  if (!res.body) return new Uint8Array(await res.arrayBuffer());
  const parts: Uint8Array[] = [];
  let received = 0;
  const reader = res.body.getReader();
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    parts.push(value);
    received += value.length;
    onProgress(received);
  }
  const out = new Uint8Array(received);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

export async function deleteFile(id: string, deleteToken: string): Promise<void> {
  await request('DELETE', filePath(id), { headers: { Authorization: `Bearer ${deleteToken}` } });
}

/** The command-line equivalent of a download, for the gpg instructions. */
export function fileUrl(id: string): string {
  return siteBase() + filePath(id);
}

function filePath(id: string): string {
  return `/api/v1/files/${encodeURIComponent(id)}`;
}

function limitsQuery(l: Limits): string {
  const q = new URLSearchParams({ expires: l.expires });
  if (l.maxDownloads > 0) q.set('max_downloads', String(l.maxDownloads));
  return q.toString();
}

async function request(
  method: string,
  path: string,
  init: { headers?: Record<string, string>; body?: Blob; signal?: AbortSignal } = {},
): Promise<Response> {
  const res = await fetch(apiBase + path, {
    method,
    headers: init.body ? { 'Content-Type': 'application/octet-stream', ...init.headers } : init.headers,
    body: init.body,
    signal: init.signal,
    // No cookies, no referrer: the API works with tokens in headers only.
    credentials: 'omit',
    referrerPolicy: 'no-referrer',
    cache: 'no-store',
  });
  if (res.ok) return res;
  let message = `${res.status} ${res.statusText}`;
  let offset: number | undefined;
  if (method !== 'HEAD') {
    try {
      const body = (await res.json()) as { error?: string; offset?: number };
      if (body.error) message = body.error;
      offset = body.offset;
    } catch {
      // Not JSON, e.g. from a proxy in between.
    }
  }
  throw new ApiError(res.status, message, offset);
}

function toUploaded(r: {
  id: string;
  delete_token: string;
  size: number;
  expires_at: string;
  max_downloads?: number;
}): Uploaded {
  return {
    id: r.id,
    deleteToken: r.delete_token,
    size: r.size,
    expiresAt: new Date(r.expires_at),
    maxDownloads: r.max_downloads ?? 0,
  };
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(resolve, ms);
    signal?.addEventListener('abort', () => {
      clearTimeout(t);
      reject(signal.reason);
    });
  });
}
