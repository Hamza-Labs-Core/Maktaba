// Full-page navigations that leave the SPA: the OAuth starts, Stripe Checkout
// and the billing portal. One seam so tests can observe them (jsdom cannot
// navigate).
export function navigateExternal(url: string): void {
  window.location.assign(url);
}
