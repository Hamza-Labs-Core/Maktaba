// maktaba.tv's shell script (story 60.01 part A): the phone menu, the copy buttons, the
// problem index's filter and the language hint's dismissal. No framework, no request.
// Every page works without it: the nav stays a row of links, the code stays selectable,
// the index stays a full list.

document.documentElement.classList.add("js");

// The phone menu: a disclosure button (aria-expanded) over the nav; Escape closes it and
// gives focus back to the button.
for (const header of document.querySelectorAll(".site-header")) {
  const button = header.querySelector(".menu-button");
  if (!button) continue;
  const set = (open) => {
    header.classList.toggle("open", open);
    button.setAttribute("aria-expanded", String(open));
  };
  button.addEventListener("click", () => set(!header.classList.contains("open")));
  header.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && header.classList.contains("open")) {
      set(false);
      button.focus();
    }
  });
  if (document.documentElement.dataset.menu === "open") set(true);
}

// Copy buttons: the block's text to the clipboard, the label to "Copied" for two seconds,
// and the same word into the page's polite live region for a screen reader.
const live = document.getElementById("announce");
for (const button of document.querySelectorAll(".copy")) {
  const label = button.querySelector(".copy-label");
  const idle = label.textContent;
  button.addEventListener("click", async () => {
    const code = button.closest(".code").querySelector("pre").innerText;
    try {
      await navigator.clipboard.writeText(code);
    } catch {
      return; // no clipboard (an old browser, an insecure origin): the text stays selectable
    }
    label.textContent = button.dataset.done;
    button.dataset.copied = "";
    if (live) live.textContent = button.dataset.done;
    setTimeout(() => {
      label.textContent = idle;
      delete button.dataset.copied;
      if (live) live.textContent = "";
    }, 2000);
  });
  if (button.hasAttribute("data-show-copied")) {
    label.textContent = button.dataset.done;
    button.dataset.copied = "";
  }
}

// The problem index's filter in the mockup: narrows the list in the page as you type, no
// request. The built index has its own (src/problems-filter.ts, with Arabic's plural forms on
// data-count-*), so this one runs only on a count that carries the mockup's data-many.
const filter = document.getElementById("filter");
if (filter && document.getElementById("count")?.dataset.many) {
  const items = [...document.querySelectorAll(".areas li")];
  const count = document.getElementById("count");
  const none = document.getElementById("no-match");
  const fold = (s) => s.toLocaleLowerCase().normalize("NFKD").replace(/[ً-ٟ]/g, "");
  const run = () => {
    const q = fold(filter.value.trim());
    let shown = 0;
    for (const li of items) {
      const hit = !q || fold(li.textContent).includes(q);
      li.hidden = !hit;
      if (hit) shown++;
    }
    for (const area of document.querySelectorAll(".areas section")) {
      area.hidden = ![...area.querySelectorAll("li")].some((li) => !li.hidden);
    }
    count.textContent = count.dataset[shown === 1 ? "one" : "many"].replace("{n}", shown);
    none.hidden = shown !== 0;
  };
  filter.addEventListener("input", run);
  run();
}

// The language hint: dismissed for good on this browser (localStorage, never a cookie).
for (const hint of document.querySelectorAll(".lang-hint")) {
  const close = hint.querySelector("button");
  close?.addEventListener("click", () => {
    hint.remove();
    try {
      localStorage.setItem("maktaba.langHint", "dismissed");
    } catch {
      // storage refused (private mode): the hint just comes back next time
    }
  });
}
