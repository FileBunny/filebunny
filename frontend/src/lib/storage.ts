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
  /** About how long the service keeps a direct upload, in days. */
  keepsDays?: number;
};

export const storages: Storage[] = [
  { name: 'local', label: 'FileBunny', short: 'FileBunny', direct: false, maxExpiryHours: 168 },
  // The server allows a day less than Filebin keeps a bin.
  { name: 'filebin', label: 'Filebin (filebin.net)', short: 'Filebin', direct: true, maxExpiryHours: 144, keepsDays: filebinKeepsDays },
];

export function storageByName(name: string | undefined): Storage | undefined {
  return storages.find((s) => s.name === (name || 'local'));
}

/** A name from the API for sentences; unknown names as they are. */
export function storageLabel(name: string): string {
  return storageByName(name)?.short ?? name;
}
