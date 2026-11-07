// Buy on a paid plugin's Browse page (story 54.19 §5). The price is the store's, from the
// signed catalog (price: the product, extra.prices: its amounts). A home linked to a store
// account gets Buy: the home asks the store for a one-time purchase intent, signed by itself,
// and the admin's browser posts it to the store's buy page in a new tab, where the buyer
// signs in and pays on the provider's page; the home then looks for the purchase every 15
// seconds. An unlinked home gets "Link this home to buy", which opens the linking here.
// Install stays beside it: a paid plugin's package is downloaded only by a home that bought
// it (a URL the store signs against the home's entitlement), so Install says so until then.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { pluginsApi, type CatalogEntry } from "../../lib/pluginsApi";
import { refusalWords } from "./PluginsSection";
import { StoreAccount } from "./StoreAccount";

/** Whether the catalog sells this plugin. */
export const isPaid = (p: CatalogEntry) => !!p.price && p.price !== "free";

/** The price to show: the buyer's usual currency when the store prices it, else the first. */
export function shownPrice(
  p: CatalogEntry,
  locale: "en" | "ar"
): { currency: string; text: string } | null {
  const prices = (p.extra?.prices as Record<string, number> | undefined) ?? {};
  const order =
    locale === "ar"
      ? ["SAR", "AED", "EGP", "USD", "EUR", "GBP"]
      : ["USD", "EUR", "GBP", "SAR", "AED", "EGP"];
  const currency =
    order.find((c) => typeof prices[c] === "number") ?? Object.keys(prices).sort()[0];
  if (!currency) return null;
  const f = new Intl.NumberFormat(locale === "ar" ? "ar-EG" : "en-US", {
    style: "currency",
    currency,
  });
  const digits = f.resolvedOptions().maximumFractionDigits ?? 2;
  return { currency, text: f.format(prices[currency]! / 10 ** digits) };
}

/** Posts the intent to the store's buy page in the tab opened by the click. */
function postToStore(win: Window, name: string, url: string, intent: string) {
  const form = document.createElement("form");
  form.method = "post";
  form.action = url;
  form.target = name;
  const input = document.createElement("input");
  input.type = "hidden";
  input.name = "intent";
  input.value = intent;
  form.appendChild(input);
  document.body.appendChild(form);
  form.submit();
  form.remove();
  win.focus();
}

export function BuyPlugin({ entry: p }: { entry: CatalogEntry }) {
  const { t, locale } = useI18n();
  const [linked, setLinked] = useState<boolean | null>(null);
  const [linking, setLinking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    pluginsApi
      .storeStatus()
      .then((s) => live && setLinked(s.linked))
      .catch(() => live && setLinked(false));
    return () => {
      live = false;
    };
  }, []);

  const price = shownPrice(p, locale);
  const buy = async () => {
    setError("");
    setNote("");
    // the tab opens on the click itself (a tab opened after a request is a blocked pop-up)
    const name = `mkt-buy-${Date.now()}`;
    const win = window.open("about:blank", name);
    if (!win) {
      setError(t("plugins.buy.popup_blocked"));
      return;
    }
    win.opener = null;
    setBusy(true);
    try {
      const a = await pluginsApi.buy(p.id, locale);
      postToStore(win, name, a.buy_url, a.intent);
      setNote(t("plugins.buy.started"));
    } catch (e) {
      win.close();
      setError(refusalWords(t, e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mkt-plugins__buy">
      {price && (
        <p className="mkt-plugins__price">
          {t(p.price === "yearly" ? "plugins.buy.price_yearly" : "plugins.buy.price_one_time", {
            price: price.text,
          })}
        </p>
      )}
      <p>{t("plugins.buy.paid_note")}</p>
      {error && <p role="alert">{error}</p>}
      {note && <p role="status">{note}</p>}
      {linked === true && (
        <Button onClick={() => void buy()} disabled={busy}>
          {t("plugins.buy.buy")}
        </Button>
      )}
      {linked === false && !linking && (
        <Button variant="ghost" onClick={() => setLinking(true)}>
          {t("plugins.buy.link")}
        </Button>
      )}
      {linked === false && linking && (
        <StoreAccount
          onLinked={() => {
            setLinked(true);
            setLinking(false);
          }}
        />
      )}
    </div>
  );
}
