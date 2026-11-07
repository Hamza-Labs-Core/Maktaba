// DNS-over-HTTPS (story 54.15 §4): a developer proves a domain with a TXT record
// `maktaba-developer=<token>`, read over Cloudflare's resolver (JSON API). Tests pass a fake
// fetch; the store never needs a resolver of its own.
export async function lookupTXT(name: string, fetcher: typeof fetch = fetch): Promise<string[]> {
  const r = await fetcher(`https://cloudflare-dns.com/dns-query?name=${encodeURIComponent(name)}&type=TXT`, {
    headers: { Accept: "application/dns-json" },
  });
  if (!r.ok) return [];
  const j = (await r.json()) as { Status?: number; Answer?: { type: number; data: string }[] };
  if (j.Status !== 0) return [];
  // a TXT answer is one or more quoted strings, joined
  return (j.Answer ?? []).filter((a) => a.type === 16).map((a) => a.data.replace(/^"|"$/g, "").replace(/"\s*"/g, ""));
}
