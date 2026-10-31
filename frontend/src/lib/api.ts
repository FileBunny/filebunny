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
  /** An external service the server passes the file on to; omitted: this server. */
  storage?: string;
};

/** The most ciphertext a share link may hold (the server's default limit). */
export const maxCiphertext = 100 << 20;

export type StorageStatus = {
  name: string;
  available: boolean;
  /** null before the first check. */
  checkedAt: Date | null;
  maxExpirySeconds: number;
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
  const done = await send(ciphertext, `/api/v1/files?${query}`, `/api/v1/uploads?${query}`, onProgress, signal);
  return toUploaded(done as Parameters<typeof toUploaded>[0]);
}

/**
 * Uploads a ciphertext stream to an upload link, like upload(). Returns
 * the stored size; the uploader gets no ID or delete token.
 */
export async function uploadToInbox(
  inboxId: string,
  ciphertext: ReadableStream<Uint8Array>,
  onProgress: Progress = () => {},
  signal?: AbortSignal,
): Promise<number> {
  const done = await send(
    ciphertext,
    `${inboxPath(inboxId)}/files`,
    `/api/v1/uploads?inbox=${encodeURIComponent(inboxId)}`,
    onProgress,
    signal,
  );
  return (done as { size: number }).size;
}

/** One request if the ciphertext fits in a chunk, a chunked upload otherwise. */
async function send(
  ciphertext: ReadableStream<Uint8Array>,
  singlePath: string,
  chunkedPath: string,
  onProgress: Progress,
  signal?: AbortSignal,
): Promise<unknown> {
  const reader = ciphertext.getReader();
  const chunks = new Chunker(reader);
  try {
    const first = await chunks.next();
    if (first.done) {
      const res = await request('POST', singlePath, { body: first.chunk, signal });
      onProgress(first.chunk.size);
      return await res.json();
    }
    return await uploadChunked(chunks, first.chunk, chunkedPath, onProgress, signal);
  } finally {
    reader.releaseLock();
  }
}

async function uploadChunked(
  chunks: Chunker,
  first: Blob,
  createPath: string,
  onProgress: Progress,
  signal?: AbortSignal,
): Promise<unknown> {
  const res = await request('POST', createPath, { signal });
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
    return await done.json();
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
  return readBody(await request('GET', filePath(id), { signal }), onProgress);
}

/** Reads a response body; more than max bytes is an error. */
export async function readBody(res: Response, onProgress: Progress = () => {}, max = Infinity): Promise<Uint8Array> {
  if (!res.body) {
    const all = new Uint8Array(await res.arrayBuffer());
    if (all.length > max) throw new RangeError('response too large');
    return all;
  }
  const parts: Uint8Array[] = [];
  let received = 0;
  const reader = res.body.getReader();
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    received += value.length;
    if (received > max) {
      void reader.cancel();
      throw new RangeError('response too large');
    }
    parts.push(value);
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

/** The external services this server offers (the status page). */
export async function storageStatus(): Promise<StorageStatus[]> {
  const r = (await (await request('GET', '/api/v1/storage')).json()) as {
    storage: { name: string; available: boolean; checked_at?: string; max_expiry_seconds: number }[];
  };
  return r.storage.map((s) => ({
    name: s.name,
    available: s.available,
    checkedAt: s.checked_at ? new Date(s.checked_at) : null,
    maxExpirySeconds: s.max_expiry_seconds,
  }));
}

/** The command-line equivalent of a download, for the gpg instructions. */
export function fileUrl(id: string): string {
  return siteBase() + filePath(id);
}

export type InboxCreated = {
  id: string;
  manageToken: string;
  expiresAt: Date;
  maxUploads: number;
  maxSize: number;
  /** How long each upload is kept after it arrives, in seconds. */
  keepSeconds: number;
  /** The external service uploads go to; undefined for this server. */
  storage?: string;
};

export type InboxInfo = {
  publicKey: string;
  expiresAt: Date;
  uploadsLeft: number;
  bytesLeft: number;
  /** The most the next upload may be; 0 when the link is full. */
  maxFileSize: number;
  /** The external service uploads go to; undefined for this server. */
  storage?: string;
};

export type InboxFile = { id: string; size: number; receivedAt: Date; expiresAt: Date };

export type InboxStatus = {
  expiresAt: Date;
  open: boolean;
  uploads: number;
  maxUploads: number;
  bytes: number;
  maxBytes: number;
  files: InboxFile[];
};

/** Registers an armored public key as an upload link. */
export async function createInbox(
  publicKey: string,
  opts: { expires: string; maxUploads: number; storage?: string },
): Promise<InboxCreated> {
  const q = new URLSearchParams({ expires: opts.expires, max_uploads: String(opts.maxUploads) });
  if (opts.storage) q.set('storage', opts.storage);
  const res = await request('POST', `/api/v1/inboxes?${q}`, { body: new Blob([publicKey]) });
  const r = (await res.json()) as {
    id: string;
    manage_token: string;
    expires_at: string;
    max_uploads: number;
    max_size: number;
    keep_seconds: number;
    storage?: string;
  };
  return {
    id: r.id,
    manageToken: r.manage_token,
    expiresAt: new Date(r.expires_at),
    maxUploads: r.max_uploads,
    maxSize: r.max_size,
    keepSeconds: r.keep_seconds,
    storage: r.storage,
  };
}

/** The public side of an upload link; null when it is gone or expired. */
export async function inboxInfo(id: string): Promise<InboxInfo | null> {
  try {
    const r = (await (await request('GET', inboxPath(id))).json()) as {
      public_key: string;
      expires_at: string;
      uploads_left: number;
      bytes_left: number;
      max_file_size: number;
      storage?: string;
    };
    return {
      publicKey: r.public_key,
      expiresAt: new Date(r.expires_at),
      uploadsLeft: r.uploads_left,
      bytesLeft: r.bytes_left,
      maxFileSize: r.max_file_size,
      storage: r.storage,
    };
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}

/** What arrived at an upload link; needs the management token. */
export async function inboxStatus(id: string, token: string): Promise<InboxStatus> {
  const res = await request('GET', `${inboxPath(id)}/files`, { headers: bearer(token) });
  const r = (await res.json()) as {
    expires_at: string;
    open: boolean;
    uploads: number;
    max_uploads: number;
    bytes: number;
    max_bytes: number;
    files: { id: string; size: number; received_at: string; expires_at: string }[];
  };
  return {
    expiresAt: new Date(r.expires_at),
    open: r.open,
    uploads: r.uploads,
    maxUploads: r.max_uploads,
    bytes: r.bytes,
    maxBytes: r.max_bytes,
    files: r.files.map((f) => ({ id: f.id, size: f.size, receivedAt: new Date(f.received_at), expiresAt: new Date(f.expires_at) })),
  };
}

export async function fetchInboxFile(id: string, token: string, fileId: string, onProgress: Progress = () => {}): Promise<Uint8Array> {
  const res = await request('GET', `${inboxPath(id)}/files/${encodeURIComponent(fileId)}`, { headers: bearer(token) });
  return readBody(res, onProgress);
}

export async function deleteInboxFile(id: string, token: string, fileId: string): Promise<void> {
  await request('DELETE', `${inboxPath(id)}/files/${encodeURIComponent(fileId)}`, { headers: bearer(token) });
}

/** Revokes the link and deletes everything it received. */
export async function revokeInbox(id: string, token: string): Promise<void> {
  await request('DELETE', inboxPath(id), { headers: bearer(token) });
}

function inboxPath(id: string): string {
  return `/api/v1/inboxes/${encodeURIComponent(id)}`;
}

function bearer(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}` };
}

function filePath(id: string): string {
  return `/api/v1/files/${encodeURIComponent(id)}`;
}

function limitsQuery(l: Limits): string {
  const q = new URLSearchParams({ expires: l.expires });
  if (l.maxDownloads > 0) q.set('max_downloads', String(l.maxDownloads));
  if (l.storage) q.set('storage', l.storage);
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
