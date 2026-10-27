import { readFileSync } from 'node:fs';

/** Reads a test key from testdata/keys (see the README there). */
export function testKey(name: string): string {
  return readFileSync(new URL(`../../testdata/keys/${name}`, import.meta.url), 'utf8');
}

export async function collect(stream: ReadableStream<Uint8Array>): Promise<Uint8Array> {
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

export function streamOf(data: Uint8Array, chunkSize = 64 * 1024): ReadableStream<Uint8Array> {
  let offset = 0;
  return new ReadableStream({
    pull(controller) {
      if (offset >= data.length) {
        controller.close();
        return;
      }
      controller.enqueue(data.slice(offset, offset + chunkSize));
      offset += chunkSize;
    },
  });
}

export function randomBytes(n: number): Uint8Array {
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i += 65536) crypto.getRandomValues(out.subarray(i, i + 65536));
  return out;
}
