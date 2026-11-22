// Proof of work before an upload, a new upload link or a mail (see
// docs/api.md): find `proofs` counters (16) so that SHA-256(challenge "."
// counter) starts with the required number of zero bits for each. Many
// small puzzles instead of one make the time predictable, and the
// progress honest. It runs in this page, in slices, so the page stays
// responsive (the single HTML file cannot use workers). A browser needs
// well under a second at the usual difficulty, a phone without a
// JavaScript JIT a few seconds; the server raises it for visitors who do
// a lot.
import { leadingZeroBits, sha256 } from './sha256.ts';

export type PowAction = 'upload' | 'inbox' | 'mail';

export type Challenge = { challenge: string; bits: number; proofs?: number };

/** Hashes per slice before giving the page a turn. */
const sliceSize = 20_000;

/** Lets the page run: a message is not throttled like setTimeout(0). */
function yieldToPage(): Promise<void> {
  return new Promise((resolve) => {
    const ch = new MessageChannel();
    ch.port1.onmessage = () => {
      ch.port1.close();
      resolve();
    };
    ch.port2.postMessage(null);
  });
}

/**
 * Finds the proof for a challenge: "challenge.counter1.counter2…".
 * onProgress gets the share done so far, from 0 to 1.
 */
export async function solve(c: Challenge, signal?: AbortSignal, onProgress: (done: number) => void = () => {}): Promise<string> {
  const want = Math.max(c.proofs ?? 1, 1);
  const found: number[] = [];
  const prefix = new TextEncoder().encode(c.challenge + '.');
  // Room for the prefix and a counter of up to 16 digits.
  const buf = new Uint8Array(prefix.length + 16);
  buf.set(prefix);
  onProgress(0);
  for (let n = 0; ; ) {
    for (const end = n + sliceSize; n < end; n++) {
      let len = prefix.length;
      const digits = String(n);
      for (let i = 0; i < digits.length; i++) buf[len++] = digits.charCodeAt(i);
      if (leadingZeroBits(sha256(buf.subarray(0, len))) >= c.bits) {
        found.push(n);
        if (found.length === want) {
          onProgress(1);
          return [c.challenge, ...found].join('.');
        }
      }
    }
    onProgress(found.length / want);
    signal?.throwIfAborted();
    await yieldToPage();
  }
}
