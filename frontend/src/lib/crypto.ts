// OpenPGP encryption and decryption in the browser. Everything here runs
// locally: passwords, private keys and plaintext never leave the page.
//
// The output must stay decryptable with GnuPG 2.4, which does not support
// RFC 9580 (v6 packets, SEIPDv2/AEAD). So: SEIPDv1 with MDC, iterated and
// salted S2K, no compression. See docs/architecture.md.
import * as openpgp from 'openpgp';

const config: openpgp.PartialConfig = {
  aeadProtect: false,
  preferredSymmetricAlgorithm: openpgp.enums.symmetric.aes256,
  preferredCompressionAlgorithm: openpgp.enums.compression.uncompressed,
  s2kType: openpgp.enums.s2k.iterated,
};

/** What to encrypt. A text message has no file name. */
export type Plaintext =
  | { kind: 'text'; text: string }
  | { kind: 'file'; name: string; data: ReadableStream<Uint8Array> | Uint8Array };

/** Who can decrypt: everyone with the password, or the holders of the keys. */
export type Recipients = { password: string } | { publicKeys: openpgp.PublicKey[] };

/** How to decrypt: a password or one or more unlocked private keys. */
export type Secret = { password: string } | { privateKeys: openpgp.PrivateKey[] };

export type Decrypted = { kind: 'text'; text: string } | { kind: 'file'; name: string; data: Uint8Array };

/**
 * Encrypts to a binary OpenPGP message. Files are encrypted as a stream, so
 * memory use does not grow with the file size.
 *
 * The literal data packet carries the file name (encrypted, so the server
 * never sees it) and the date 0, so no timestamp is revealed. Recipient key
 * IDs are hidden (like `gpg --throw-keyids`): the server cannot tell whose
 * key a message is for.
 */
export async function encrypt(plaintext: Plaintext, to: Recipients): Promise<ReadableStream<Uint8Array>> {
  const date = new Date(0);
  const message =
    plaintext.kind === 'text'
      ? // As bytes: createMessage({ text }) would turn every \n into \r\n.
        await openpgp.createMessage({ binary: new TextEncoder().encode(plaintext.text), format: 'utf8', date })
      : await openpgp.createMessage({
          binary: plaintext.data,
          filename: literalFileName(plaintext.name),
          format: 'binary',
          date,
        });
  const encrypted: ReadableStream<Uint8Array> | Uint8Array = await openpgp.encrypt({
    message,
    format: 'binary',
    config,
    ...('password' in to ? { passwords: [to.password] } : { encryptionKeys: to.publicKeys, wildcard: true }),
  });
  return encrypted instanceof Uint8Array ? toStream(encrypted) : encrypted;
}

/**
 * Decrypts a whole message (binary or ASCII-armored). The integrity check
 * (MDC) covers the whole message, so nothing is returned before all of it
 * has been checked. A message without a file name is a text message, as
 * with `echo … | gpg --encrypt`.
 */
export async function decrypt(ciphertext: Uint8Array, secret: Secret): Promise<Decrypted> {
  const message = await readMessage(ciphertext);
  const { data, filename } = await openpgp.decrypt({
    message,
    format: 'binary',
    config,
    ...('password' in secret ? { passwords: [secret.password] } : { decryptionKeys: secret.privateKeys }),
  });
  if (!filename) {
    try {
      return { kind: 'text', text: new TextDecoder('utf-8', { fatal: true }).decode(data) };
    } catch {
      // Not valid UTF-8: offer it as a file instead.
    }
  }
  return { kind: 'file', name: safeFileName(filename), data };
}

/** Tells whether a message opens with a password, a private key, or both. */
export async function messageInfo(ciphertext: Uint8Array): Promise<{ password: boolean; key: boolean }> {
  const message = await readMessage(ciphertext);
  const { symEncryptedSessionKey, publicKeyEncryptedSessionKey } = openpgp.enums.packet;
  return {
    password: message.packets.filterByTag(symEncryptedSessionKey).length > 0,
    key: message.packets.filterByTag(publicKeyEncryptedSessionKey).length > 0,
  };
}

export type KeyInfo = {
  key: openpgp.PublicKey;
  fingerprint: string;
  userIDs: string[];
  canEncrypt: boolean;
};

/**
 * Key files (.asc, .gpg) come in as bytes, but are often ASCII-armored.
 * Armored bytes are decoded to text so OpenPGP.js reads them as armor.
 */
function armoredOrBinary(input: string | Uint8Array): string | Uint8Array {
  if (typeof input === 'string') return input;
  const text = new TextDecoder().decode(input);
  return text.includes('-----BEGIN PGP') ? text : input;
}

/**
 * Reads one or more public keys (armored or binary). Private keys are
 * refused, so a user who pastes the wrong block learns about it.
 */
export async function readPublicKeys(file: string | Uint8Array): Promise<KeyInfo[]> {
  const input = armoredOrBinary(file);
  // OpenPGP.js reads only the first armored block; read every one, so a
  // second key in the same file or response is never silently ignored.
  const keys =
    typeof input === 'string'
      ? (await Promise.all(armorBlocks(input).map((armoredKeys) => openpgp.readKeys({ armoredKeys })))).flat()
      : await openpgp.readKeys({ binaryKeys: input });
  return Promise.all(
    keys.map(async (key) => {
      if (key.isPrivate()) {
        throw new Error('This is a private key. Use the public key of the recipient.');
      }
      return {
        key,
        fingerprint: formatFingerprint(key.getFingerprint()),
        userIDs: key.getUserIDs(),
        canEncrypt: await key.getEncryptionKey().then(
          () => true,
          () => false,
        ),
      };
    }),
  );
}

/** Splits text into its "-----BEGIN PGP" blocks (the whole text if none). */
function armorBlocks(text: string): string[] {
  const blocks = text.match(/-----BEGIN PGP [^-]+-----[\s\S]*?-----END PGP [^-]+-----/g);
  return blocks ?? [text];
}

/**
 * Reads a private key and unlocks it with the passphrase if it has one. The
 * key stays in memory in this page only.
 */
export async function readPrivateKey(file: string | Uint8Array, passphrase: string): Promise<openpgp.PrivateKey> {
  const input = armoredOrBinary(file);
  const key =
    typeof input === 'string'
      ? await openpgp.readPrivateKey({ armoredKey: input })
      : await openpgp.readPrivateKey({ binaryKey: input });
  if (key.isDecrypted()) return key;
  return openpgp.decryptKey({ privateKey: key, passphrase });
}

/** Groups a hex fingerprint in blocks of four, as gpg shows it. */
export function formatFingerprint(hex: string): string {
  return (
    hex
      .toUpperCase()
      .match(/.{1,4}/g)
      ?.join(' ') ?? ''
  );
}

/**
 * Makes a decrypted file name safe to offer as a download: no directories,
 * no control characters, not empty.
 */
export function safeFileName(name: string | null | undefined): string {
  const base = (name ?? '').split(/[\\/]/).pop()!.replace(/[\u0000-\u001f\u007f]/g, '').trim();
  return base === '' || base === '.' || base === '..' ? 'download' : base;
}

// The literal data packet stores at most 255 bytes of file name. Shorten
// long names, keeping the extension.
function literalFileName(name: string): string {
  const enc = new TextEncoder();
  if (enc.encode(name).length <= 255) return name;
  const dot = name.lastIndexOf('.');
  const ext = dot > 0 && name.length - dot <= 16 ? name.slice(dot) : '';
  let stem = [...name.slice(0, name.length - ext.length)];
  while (enc.encode(stem.join('') + ext).length > 255) stem.pop();
  return stem.join('') + ext;
}

function readMessage(data: Uint8Array): Promise<openpgp.Message<Uint8Array>> {
  const prefix = new TextDecoder().decode(data.subarray(0, 64)).trimStart();
  return prefix.startsWith('-----BEGIN PGP ')
    ? openpgp.readMessage({ armoredMessage: new TextDecoder().decode(data) })
    : openpgp.readMessage({ binaryMessage: data });
}

function toStream(data: Uint8Array): ReadableStream<Uint8Array> {
  return new ReadableStream({
    start(controller) {
      controller.enqueue(data);
      controller.close();
    },
  });
}
