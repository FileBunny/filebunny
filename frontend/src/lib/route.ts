// Routes live in the URL fragment (#/d/<id>), so the same links work on the
// site and in the downloadable single HTML file opened from file://. The
// fragment is never sent to the server.

export type Route =
  | { page: 'send' }
  | { page: 'download'; id: string }
  | { page: 'delete'; id: string; token: string }
  | { page: 'receive' }
  | { page: 'key' }
  /** fingerprint: lowercase hex, checked against the key the server sends. */
  | { page: 'inbox-upload'; id: string; fingerprint: string }
  | { page: 'inbox-manage'; id: string; token: string }
  | { page: 'not-found' };

const id = '([A-Za-z0-9_-]{22})';
const token = '([A-Za-z0-9_-]{43})';
// v4 (40 hex) or v6 (64 hex) fingerprints.
const fingerprint = '([0-9a-f]{40}|[0-9a-f]{64})';

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, '');
  if (path === '' || path === '/') return { page: 'send' };
  let m = path.match(new RegExp(`^/d/${id}$`));
  if (m) return { page: 'download', id: m[1]! };
  m = path.match(new RegExp(`^/delete/${id}/${token}$`));
  if (m) return { page: 'delete', id: m[1]!, token: m[2]! };
  if (path === '/receive') return { page: 'receive' };
  if (path === '/key') return { page: 'key' };
  m = path.match(new RegExp(`^/u/${id}/${fingerprint}$`));
  if (m) return { page: 'inbox-upload', id: m[1]!, fingerprint: m[2]! };
  m = path.match(new RegExp(`^/m/${id}/${token}$`));
  if (m) return { page: 'inbox-manage', id: m[1]!, token: m[2]! };
  return { page: 'not-found' };
}

export function shareLink(base: string, fileId: string): string {
  return `${base}/#/d/${fileId}`;
}

export function deleteLink(base: string, fileId: string, deleteToken: string): string {
  return `${base}/#/delete/${fileId}/${deleteToken}`;
}

/** Fingerprint as it goes into a link: lowercase hex without spaces. */
export function linkFingerprint(fingerprint: string): string {
  return fingerprint.replace(/\s+/g, '').toLowerCase();
}

/**
 * The public upload link. It carries the key's fingerprint, so the upload
 * page can check the key the server sends: a server that swapped the key
 * would be caught.
 */
export function uploadLink(base: string, inboxId: string, fingerprint: string): string {
  return `${base}/#/u/${inboxId}/${linkFingerprint(fingerprint)}`;
}

export function manageLink(base: string, inboxId: string, manageToken: string): string {
  return `${base}/#/m/${inboxId}/${manageToken}`;
}
