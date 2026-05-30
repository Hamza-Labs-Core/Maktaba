// Shared class-name joiner for @maktaba/design-system primitives.
// Kept dependency-free (no `clsx`/`classnames` npm pull) so the design
// system stays installable without a transitive runtime dep. Every
// primitive imports this instead of redefining a local copy.

export function clsx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}
