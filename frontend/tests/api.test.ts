import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { ApiError, chunkSize, inboxInfo, inboxStatus, upload, uploadToInbox } from '../src/lib/api.ts';
import { randomBytes, streamOf } from './helpers.ts';

type Call = { method: string; url: string; size: number };

/**
 * A fake API in memory, enough for upload(): single uploads, chunked
 * sessions with offsets, complete and abort. fail(n) makes the next n chunk
 * requests fail after the server stored them (a lost response).
 */
function fakeServer() {
  const calls: Call[] = [];
  let stored = new Uint8Array();
  let lostResponses = 0;
  const ok = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  const created = (size: number) =>
    ok({ id: 'f'.repeat(22), delete_token: 't', size, expires_at: '2026-10-28T12:00:00Z', max_downloads: 1 }, 201);

  globalThis.fetch = async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    const body = init?.body instanceof Blob ? new Uint8Array(await init.body.arrayBuffer()) : new Uint8Array();
    calls.push({ method, url, size: body.length });

    if (method === 'POST' && url.startsWith('/api/v1/files?')) {
      stored = body;
      return created(body.length);
    }
    if (method === 'POST' && url === `/api/v1/inboxes/${'i'.repeat(22)}/files`) {
      stored = body;
      return ok({ size: body.length }, 201);
    }
    if (method === 'POST' && url.startsWith('/api/v1/uploads?')) {
      return ok({ id: 'u'.repeat(22), upload_token: 'secret', max_chunk_size: 16 << 20, max_size: 100 << 20 }, 201);
    }
    if (method === 'PUT') {
      const offset = Number(new URL(url, 'http://x').searchParams.get('offset'));
      if (offset !== stored.length) return ok({ error: 'wrong offset', offset: stored.length }, 409);
      const next = new Uint8Array(stored.length + body.length);
      next.set(stored);
      next.set(body, stored.length);
      stored = next;
      if (lostResponses > 0) {
        lostResponses--;
        throw new TypeError('network error');
      }
      return ok({ offset: stored.length });
    }
    if (method === 'POST' && url.endsWith('/complete')) return created(stored.length);
    if (method === 'DELETE') return new Response(null, { status: 204 });
    return ok({ error: 'not found' }, 404);
  };
  return {
    calls,
    stored: () => stored,
    loseResponses: (n: number) => (lostResponses = n),
  };
}

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

const limits = { expires: '1h', maxDownloads: 1 };

test('small message: one request with the limits', async () => {
  const srv = fakeServer();
  const up = await upload(streamOf(new Uint8Array([1, 2, 3])), limits);
  assert.equal(up.maxDownloads, 1);
  assert.deepEqual(
    srv.calls.map((c) => `${c.method} ${c.url}`),
    ['POST /api/v1/files?expires=1h&max_downloads=1'],
  );
});

test('large file: exact chunks, then complete', async () => {
  const srv = fakeServer();
  const data = randomBytes(2 * chunkSize + 12345);
  const sent: number[] = [];
  // Stream pieces that do not line up with chunk boundaries.
  const up = await upload(streamOf(data, 3 * 1024 * 1024 + 7), { expires: '24h', maxDownloads: 0 }, (n) => sent.push(n));
  assert.equal(up.size, data.length);
  assert.ok(Buffer.from(srv.stored()).equals(Buffer.from(data)));
  assert.deepEqual(
    srv.calls.map((c) => `${c.method} ${c.url.replace(/\?.*/, '')} ${c.size}`),
    [
      'POST /api/v1/uploads 0',
      `PUT /api/v1/uploads/${'u'.repeat(22)} ${chunkSize}`,
      `PUT /api/v1/uploads/${'u'.repeat(22)} ${chunkSize}`,
      `PUT /api/v1/uploads/${'u'.repeat(22)} 12345`,
      `POST /api/v1/uploads/${'u'.repeat(22)}/complete 0`,
    ],
  );
  assert.equal(srv.calls[0]?.url, '/api/v1/uploads?expires=24h');
  assert.deepEqual(sent, [chunkSize, 2 * chunkSize, data.length]);
});

test('external storage goes in the query of both kinds of upload', async () => {
  const creates: string[] = [];
  for (const size of [1, chunkSize + 1]) {
    const srv = fakeServer();
    await upload(streamOf(randomBytes(size)), { ...limits, storage: 'filebin' });
    creates.push(srv.calls[0]!.url);
  }
  assert.deepEqual(creates, [
    '/api/v1/files?expires=1h&max_downloads=1&storage=filebin',
    '/api/v1/uploads?expires=1h&max_downloads=1&storage=filebin',
  ]);
});

test('exactly one chunk: no empty chunk is sent', async () => {
  const srv = fakeServer();
  await upload(streamOf(randomBytes(chunkSize)), limits);
  // One chunk fills the buffer, so the stream end is only seen afterwards.
  assert.deepEqual(
    srv.calls.map((c) => c.method),
    ['POST', 'PUT', 'POST'],
  );
});

test('a lost chunk response is recovered through the 409 offset', async () => {
  const srv = fakeServer();
  const data = randomBytes(chunkSize + 10);
  srv.loseResponses(1);
  await upload(streamOf(data), limits);
  assert.ok(Buffer.from(srv.stored()).equals(Buffer.from(data)));
});

test('a failing upload is aborted on the server', async () => {
  const srv = fakeServer();
  globalThis.fetch = (async (input: string, init?: RequestInit) => {
    srv.calls.push({ method: init?.method ?? 'GET', url: String(input), size: 0 });
    if (init?.method === 'POST') {
      return new Response(JSON.stringify({ id: 'u', upload_token: 's', max_chunk_size: 1, max_size: 1 }), { status: 201 });
    }
    if (init?.method === 'PUT') return new Response(JSON.stringify({ error: 'too large' }), { status: 413 });
    return new Response(null, { status: 204 });
  }) as typeof fetch;
  await assert.rejects(upload(streamOf(randomBytes(chunkSize + 1)), limits), ApiError);
  assert.equal(srv.calls.at(-1)?.method, 'DELETE');
});

const inbox = 'i'.repeat(22);

test('upload link: small message in one request, large file chunked with ?inbox=', async () => {
  const server = fakeServer();
  assert.equal(await uploadToInbox(inbox, streamOf(randomBytes(100))), 100);
  assert.deepEqual(server.calls.map((c) => `${c.method} ${c.url}`), [`POST /api/v1/inboxes/${inbox}/files`]);

  const big = fakeServer();
  const size = chunkSize + 5;
  assert.equal(await uploadToInbox(inbox, streamOf(randomBytes(size))), size);
  assert.equal(big.calls[0]!.url, `/api/v1/uploads?inbox=${inbox}`);
  assert.ok(big.calls.at(-1)!.url.endsWith('/complete'));
});

test('upload link info and status', async () => {
  const seen: RequestInit[] = [];
  globalThis.fetch = async (input: string | URL | Request, init?: RequestInit) => {
    seen.push(init ?? {});
    const url = String(input);
    if (url === '/api/v1/inboxes/gone') return new Response('{"error":"not found"}', { status: 404 });
    if (url.endsWith('/files')) {
      return new Response(
        JSON.stringify({
          expires_at: '2026-11-05T12:00:00Z',
          open: true,
          uploads: 1,
          max_uploads: 10,
          bytes: 14,
          max_bytes: 100,
          files: [{ id: 'f', size: 14, received_at: '2026-10-29T12:00:00Z', expires_at: '2026-11-05T12:00:00Z' }],
        }),
      );
    }
    return new Response(
      JSON.stringify({ public_key: 'K', expires_at: '2026-11-05T12:00:00Z', uploads_left: 9, bytes_left: 86, max_file_size: 86 }),
    );
  };
  assert.equal(await inboxInfo('gone'), null);
  const info = await inboxInfo(inbox);
  assert.equal(info?.publicKey, 'K');
  assert.equal(info?.maxFileSize, 86);
  const st = await inboxStatus(inbox, 'manage-token');
  assert.equal(st.files[0]!.receivedAt.toISOString(), '2026-10-29T12:00:00.000Z');
  assert.deepEqual(seen.at(-1)!.headers, { Authorization: 'Bearer manage-token' });
  assert.equal(seen.at(-1)!.credentials, 'omit');
});
