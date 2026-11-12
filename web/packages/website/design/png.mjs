// The chunk types of a PNG, in order (story 60.01 T01: the renders carry no metadata).

const SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

/** @param {Buffer} buf */
export function pngChunks(buf) {
  if (!buf.subarray(0, 8).equals(SIGNATURE)) throw new Error("not a PNG");
  const types = [];
  let at = 8;
  while (at + 8 <= buf.length) {
    const length = buf.readUInt32BE(at);
    types.push(buf.toString("latin1", at + 4, at + 8));
    at += 12 + length;
  }
  return types;
}
