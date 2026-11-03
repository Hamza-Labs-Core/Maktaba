// Full-page navigations that leave the SPA: the OAuth starts, the payment
// provider's checkout and billing portal. One seam so tests can observe them
// (jsdom cannot navigate).
export function navigateExternal(url: string): void {
  window.location.assign(url);
}
