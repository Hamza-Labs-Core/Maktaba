// The index filter (Epic 60 story 60.06, T11; story 60.12, T08): /problems/ lists every message
// and /help/ every help topic (and their /ar/ twins), and typing in the box narrows the list in
// the page. It sends no request: it reads the list that is already there, the element marked
// data-filter-list, whose sections are its groups and whose items are matched on their text and
// their data-search (a help topic's headings). Without JavaScript the box is hidden (the index's
// <noscript> stylesheet) and the whole list shows.
//
// The build publishes this file with its types stripped, under a content-hashed name
// (scripts/build.mjs), as the index's one script. It imports nothing, so it runs as it is in the
// browser and in the tests (problems-filter.test.ts).

/** A text as the filter compares it: lower case, Arabic without its short vowels and tatweel,
 * the alef forms as one, ta marbuta as ha and alef maqsura as ya, the separators of slugs and
 * codes as spaces, and runs of space as one. */
export function fold(text: string): string {
  return text
    .toLocaleLowerCase("en")
    .replace(/[ً-ٰٟـ]/g, "")
    .replace(/[أإآٱ]/g, "ا")
    .replace(/ة/g, "ه")
    .replace(/ى/g, "ي")
    .replace(/[_\-/]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

/** Whether an entry's text has every word of the query, in any order (an empty query matches). */
export function matches(query: string, text: string): boolean {
  const words = fold(query).split(" ").filter(Boolean);
  const hay = fold(text);
  return words.every((w) => hay.includes(w));
}

/** The count line for n entries, from the element's data-count-<plural form> attributes. */
export function countText(el: { lang: string; dataset: DOMStringMap }, n: number): string {
  const form = new Intl.PluralRules(el.lang || "en").select(n);
  const zero = n === 0 ? el.dataset.countZero : undefined;
  const key = `count${form[0].toUpperCase()}${form.slice(1)}`;
  const text = zero ?? el.dataset[key] ?? el.dataset.countOther ?? "{n}";
  return text.replace("{n}", String(n));
}

/** Wires the filter on the page: on each input it hides the entries that do not match and the
 * areas left empty, and updates the count and the "no match" line. The box is in place from the
 * first paint, so wiring it moves nothing. */
export function enhance(doc: Document): void {
  const input = doc.getElementById("filter") as HTMLInputElement | null;
  const count = doc.getElementById("count");
  const none = doc.getElementById("no-match");
  if (!input || !count || !none) return;
  const items = [...doc.querySelectorAll<HTMLLIElement>("[data-filter-list] li")].map((li) => ({
    li,
    text: `${li.textContent ?? ""} ${li.dataset.search ?? ""}`,
  }));
  const areas = [...doc.querySelectorAll<HTMLElement>("[data-filter-list] section")];
  const apply = () => {
    let shown = 0;
    for (const it of items) {
      const ok = matches(input.value, it.text);
      it.li.hidden = !ok;
      if (ok) shown++;
    }
    for (const a of areas) a.hidden = !a.querySelector("li:not([hidden])");
    count.textContent = countText(count, shown);
    none.hidden = shown > 0;
  };
  input.addEventListener("input", apply);
  // a value the browser restored (back to the page) filters at once
  if (input.value) apply();
}

if (typeof document !== "undefined") enhance(document);
