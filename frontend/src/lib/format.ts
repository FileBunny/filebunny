export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KiB', 'MiB', 'GiB'];
  let v = n;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

/**
 * A neutral file name for something made in the browser, e.g.
 * "photo-04821.jpg": a random number, so a second one does not overwrite
 * the first in the recipient's downloads. No date or device.
 */
export function neutralName(prefix: string, ext: string): string {
  // Rejection sampling: 100000 does not divide 2^32, so plain modulo would
  // be (very slightly) biased.
  const limit = Math.floor(2 ** 32 / 100_000) * 100_000;
  const n = new Uint32Array(1);
  do crypto.getRandomValues(n);
  while (n[0]! >= limit);
  return `${prefix}-${String(n[0]! % 100_000).padStart(5, '0')}.${ext}`;
}

export function formatDate(d: Date): string {
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

/** A random password of about 100 bits, without look-alike characters. */
/** Below this, a typed password gets a warning; generated ones are 23. */
export const minPasswordLength = 12;

export function generatePassword(): string {
  const alphabet = 'abcdefghijkmnpqrstuvwxyz23456789'; // 32 characters, 5 bits each
  const bytes = crypto.getRandomValues(new Uint8Array(20));
  const chars = [...bytes].map((b) => alphabet[b % 32]); // 256 is a multiple of 32: no bias
  return [0, 5, 10, 15].map((i) => chars.slice(i, i + 5).join('')).join('-');
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
