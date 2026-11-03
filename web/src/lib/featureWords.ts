// The sentences that say a feature is off (Epic 51, story 51.11). Each known feature
// has its own whole sentence (capabilities.off.<id>, capabilities.unavailable.<id>)
// so the verb agrees with the name: "Transcripts are off", «البث المباشر متوقف».
// A feature this build has no sentence for reads through a frame whose head noun
// carries the agreement ("The {feature} feature is off", «ميزة {feature} متوقفة»).
// The Android and Apple apps carry the same words.

type T = (key: string, vars?: Record<string, string | number>) => string;

function has(t: T, key: string): boolean {
  const w = t(key);
  return !!w && w !== key;
}

/** The feature's name, or its id when this build has none. */
export function featureName(id: string, t: T): string {
  const key = `components.feature.${id}`;
  return has(t, key) ? t(key) : id;
}

/** "Live TV is off on this home." */
export function featureOffText(id: string, t: T): string {
  const key = `capabilities.off.${id}`;
  return has(t, key) ? t(key) : t("capabilities.offAdmin", { feature: featureName(id, t) });
}

/** "Live TV isn't available on this home." (a viewer who followed a link to it) */
export function featureUnavailableText(id: string, t: T): string {
  const key = `capabilities.unavailable.${id}`;
  return has(t, key) ? t(key) : t("capabilities.unavailable", { feature: featureName(id, t) });
}
