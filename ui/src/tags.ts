// v10 tags (docs/DESIGN_V10_TAGS.md "UI"). PURE — no React, no DOM, no
// runtime imports — so `node --test` can load it directly (see
// ui/test/tags.test.ts). The server's model.NormalizeTags is the authority;
// this mirrors its rules for client-side preview only.

/** localStorage key for the global, cross-view tag filter. */
export const TAG_FILTER_KEY = "taskman-tagfilter";

export const MAX_TAG_LEN = 30;
export const MAX_TAGS = 20;

export type NormalizedTag = { ok: true; tag: string } | { ok: false; error: string };

/**
 * Normalise one raw tag the way the server does: trim → lowercase. An empty
 * result is `{ok:true, tag:""}` (the caller drops it). Whitespace, `,`, `#`,
 * or more than 30 runes → `{ok:false}` with the server's message shape.
 */
export function normalizeTag(raw: string): NormalizedTag {
  const tag = raw.trim().toLowerCase();
  if (tag === "") return { ok: true, tag };
  if (/[\s,#]/.test(tag)) return { ok: false, error: `invalid tag ${JSON.stringify(tag)}` };
  if ([...tag].length > MAX_TAG_LEN) return { ok: false, error: `invalid tag ${JSON.stringify(tag)}` };
  return { ok: true, tag };
}
