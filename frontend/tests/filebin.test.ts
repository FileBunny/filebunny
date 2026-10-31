import assert from 'node:assert/strict';
import { test } from 'node:test';
import { maxCiphertext } from '../src/lib/api.ts';
import { binPattern, filebinFetch, filebinFileUrl, filebinUpload, newBin } from '../src/lib/filebin.ts';

const bin = '0123456789abcdef0123456789abcdef';

test('bin names are random 128-bit hex', () => {
  const a = newBin();
  assert.match(a, binPattern);
  assert.notEqual(a, newBin());
  assert.throws(() => filebinFileUrl('../api'));
  assert.equal(filebinFileUrl(bin), `https://filebin.net/${bin}/data.bin`);
});

test('upload is a CORS simple request', async () => {
  let seen: { url: string; init: RequestInit } | undefined;
  const fetch = async (url: string | URL | Request, init?: RequestInit) => {
    seen = { url: String(url), init: init! };
    return new Response('{}', { status: 201 });
  };
  await filebinUpload(bin, new Blob([new Uint8Array([1, 2, 3])], { type: 'application/pgp-encrypted' }), undefined, { fetch });
  assert.equal(seen!.url, `https://filebin.net/${bin}/data.bin`);
  assert.equal(seen!.init.method, 'POST');
  assert.equal(seen!.init.headers, undefined);
  assert.equal(seen!.init.credentials, 'omit');
  assert.equal(seen!.init.referrerPolicy, 'no-referrer');
  // An untyped Blob: the browser sends no Content-Type.
  assert.equal((seen!.init.body as Blob).type, '');

  const refused = async () => new Response('', { status: 403 });
  await assert.rejects(filebinUpload(bin, new Blob(['x']), undefined, { fetch: refused }));
  await assert.rejects(filebinUpload(bin, new Blob([new Uint8Array(maxCiphertext + 1)]), undefined, { fetch }));
});

test('download: data, verification page, gone, blocked', async () => {
  const answer = (res: Response | Error) => async () => {
    if (res instanceof Error) throw res;
    return res;
  };
  const data = await filebinFetch(bin, undefined, undefined, {
    fetch: answer(new Response(new Uint8Array([1, 2, 3]), { headers: { 'Content-Type': 'application/octet-stream' } })),
  });
  assert.deepEqual(data, { kind: 'data', data: new Uint8Array([1, 2, 3]) });

  const page = new Response('<html>verify</html>', { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
  assert.deepEqual(await filebinFetch(bin, undefined, undefined, { fetch: answer(page) }), { kind: 'tab' });
  assert.deepEqual(await filebinFetch(bin, undefined, undefined, { fetch: answer(new TypeError('CORS')) }), { kind: 'tab' });
  assert.deepEqual(await filebinFetch(bin, undefined, undefined, { fetch: answer(new Response('', { status: 404 })) }), {
    kind: 'gone',
  });
});

test('download refuses more than a share link can hold', async () => {
  const fetch = async () => new Response(new Uint8Array(maxCiphertext + 1));
  await assert.rejects(filebinFetch(bin, undefined, undefined, { fetch }), RangeError);
});
