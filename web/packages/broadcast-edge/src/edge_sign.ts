// The edge's signature on its posts to api.maktaba.tv (usage reports, 50.20 §3), with
// EDGE_SIGNING_KEY (an Ed25519 seed; the cloud keeps the public half as broadcast.edge_public_key
// and checks it with cloud/internal/broadcast.VerifyEdgeRequest):
//
//   X-Maktaba-Edge-Time:      unix ms
//   X-Maktaba-Edge-Signature: b64url(Ed25519("maktaba-broadcast-edge-v1\n<METHOD>\n<path>\n<ms>\n<hex sha256 body>"))
import { b64url, fromB64url, importEd25519Seed, sha256Hex, utf8 } from "./crypto";

export const EDGE_DOMAIN = "maktaba-broadcast-edge-v1";
export const HEADER_EDGE_TIME = "X-Maktaba-Edge-Time";
export const HEADER_EDGE_SIGNATURE = "X-Maktaba-Edge-Signature";

export async function edgeSigningInput(
  method: string,
  path: string,
  ms: number,
  body: Uint8Array
): Promise<string> {
  return `${EDGE_DOMAIN}\n${method.toUpperCase()}\n${path}\n${ms}\n${await sha256Hex(body)}`;
}

/** signEdgeRequest returns the two headers for a post signed with the seed (base64url). */
export async function signEdgeRequest(
  seedB64: string,
  method: string,
  path: string,
  ms: number,
  body: Uint8Array
): Promise<Record<string, string>> {
  const key = await importEd25519Seed(fromB64url(seedB64));
  const sig = await crypto.subtle.sign(
    "Ed25519",
    key,
    utf8(await edgeSigningInput(method, path, ms, body))
  );
  return { [HEADER_EDGE_TIME]: String(ms), [HEADER_EDGE_SIGNATURE]: b64url(new Uint8Array(sig)) };
}
