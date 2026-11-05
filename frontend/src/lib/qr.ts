// QR codes for fingerprints, so a fingerprint shown here can be compared
// with a phone. The content is OPENPGP4FPR:<fingerprint>, which OpenPGP
// apps such as OpenKeychain understand; any QR reader shows the text.
import qrcode from 'qrcode-generator';

/** The QR content for a fingerprint (spaces allowed). */
export function fingerprintUri(fingerprint: string): string {
  const hex = fingerprint.replace(/\s+/g, '').toUpperCase();
  if (!/^([0-9A-F]{40}|[0-9A-F]{64})$/.test(hex)) throw new Error('not a fingerprint');
  return `OPENPGP4FPR:${hex}`;
}

/** The modules of a QR code: true is dark. Error correction level M. */
export function qrModules(text: string): boolean[][] {
  const qr = qrcode(0, 'M');
  qr.addData(text, 'Byte');
  qr.make();
  const n = qr.getModuleCount();
  return Array.from({ length: n }, (_, r) => Array.from({ length: n }, (_, c) => qr.isDark(r, c)));
}
