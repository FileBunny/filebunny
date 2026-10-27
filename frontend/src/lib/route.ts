// Routes live in the URL fragment (#/d/<id>), so the same links work on the
// site and in the downloadable single HTML file opened from file://. The
// fragment is never sent to the server.

export type Route =
  | { page: 'send' }
  | { page: 'download'; id: string }
  | { page: 'delete'; id: string; token: string }
  | { page: 'not-found' };

const id = '([A-Za-z0-9_-]{22})';
const token = '([A-Za-z0-9_-]{43})';

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, '');
  if (path === '' || path === '/') return { page: 'send' };
  let m = path.match(new RegExp(`^/d/${id}$`));
  if (m) return { page: 'download', id: m[1]! };
  m = path.match(new RegExp(`^/delete/${id}/${token}$`));
  if (m) return { page: 'delete', id: m[1]!, token: m[2]! };
  return { page: 'not-found' };
}

export function shareLink(base: string, fileId: string): string {
  return `${base}/#/d/${fileId}`;
}

export function deleteLink(base: string, fileId: string, deleteToken: string): string {
  return `${base}/#/delete/${fileId}/${deleteToken}`;
}
