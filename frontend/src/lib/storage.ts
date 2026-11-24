// The places a share link's ciphertext can be kept (docs/storage/). The
// pages build their choices from this list, so a new adapter is one entry
// here plus its own module, like filebin.ts.
import { filebinKeepsDays } from './filebin.ts';

export type Storage = {
  /** The name the API uses; 'local' is this server. */
  name: string;
  /** For choices: the service and its site. */
  label: string;
  /** For sentences. */
  short: string;
  /** Whether the browser can upload there itself, without FileBunny. */
  direct: boolean;
  /** The longest expiry through FileBunny, in hours. */
  maxExpiryHours: number;
  /** Whether upload links may store there. */
  uploadLinks: boolean;
  /** About how long the service keeps a direct upload, in days. */
  keepsDays?: number;
  /**
   * For a service that cannot delete: how long it keeps a file after the
   * upload, in hours, whatever FileBunny's expiry or download limit.
   */
  keepsHours?: number;
};

export const storages: Storage[] = [
  { name: 'local', label: 'FileBunny', short: 'FileBunny', direct: false, maxExpiryHours: 168, uploadLinks: true },
  // The server allows a day less than Filebin keeps a bin.
  {
    name: 'filebin', label: 'Filebin (filebin.net)', short: 'Filebin', direct: true, maxExpiryHours: 144, uploadLinks: true,
    keepsDays: filebinKeepsDays,
  },
  // Uguu keeps every file for 3 hours and cannot delete it; the server
  // allows 2 hours. No CORS, so never directly; too short for upload links.
  {
    name: 'uguu', label: 'Uguu (uguu.se)', short: 'Uguu', direct: false, maxExpiryHours: 2, uploadLinks: false,
    keepsHours: 3,
  },
];

export function storageByName(name: string | undefined): Storage | undefined {
  return storages.find((s) => s.name === (name || 'local'));
}

/** A name from the API for sentences; unknown names as they are. */
export function storageLabel(name: string): string {
  return storageByName(name)?.short ?? name;
}

/**
 * Whether the server stores at name itself: always for 'local', for a
 * service only when GET /api/v1/storage lists it (its adapter is on).
 * offered is null while that is not known; the choice stays open then,
 * and the server refuses with 400 if it does not offer it.
 */
export function throughServer(name: string, offered: ReadonlySet<string> | null): boolean {
  return name === 'local' || offered === null || offered.has(name);
}

/**
 * The choices of a page: what the server stores at, and with withDirect
 * also the services the browser can upload to itself.
 */
export function storageChoices(offered: ReadonlySet<string> | null, withDirect: boolean): Storage[] {
  return storages.filter((s) => throughServer(s.name, offered) || (withDirect && s.direct));
}

/** The choices for an upload link: through this server, and only where links may store. */
export function uploadLinkChoices(offered: ReadonlySet<string> | null): Storage[] {
  return storageChoices(offered, false).filter((s) => s.uploadLinks);
}

const expiryOptions = [1, 2, 24, 72];

/** "1 hour", "2 hours", "1 day", "6 days". */
export function hoursLabel(hours: number): string {
  if (hours < 24) return hours === 1 ? '1 hour' : `${hours} hours`;
  const days = hours / 24;
  return days === 1 ? '1 day' : `${days} days`;
}

/** The expiries to choose from at s: the usual ones up to its maximum, and the maximum. */
export function expiryChoices(s: Storage): { value: string; label: string }[] {
  const hours = expiryOptions.filter((h) => h < s.maxExpiryHours && (h !== 2 || s.maxExpiryHours < 24));
  return [...hours, s.maxExpiryHours].map((h) => ({ value: `${h}h`, label: hoursLabel(h) }));
}
