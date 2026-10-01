// Runnable check for tags.ts (docs/DESIGN_V10_TAGS.md "Tests → UI").
import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizeTag, TAG_FILTER_KEY } from "../src/tags.ts";

test("trims and lowercases", () => {
  assert.deepEqual(normalizeTag("  Backend "), { ok: true, tag: "backend" });
  assert.deepEqual(normalizeTag("Q3-Plan_x"), { ok: true, tag: "q3-plan_x" });
});

test("empty and whitespace-only normalise to empty (caller drops)", () => {
  assert.deepEqual(normalizeTag(""), { ok: true, tag: "" });
  assert.deepEqual(normalizeTag("   "), { ok: true, tag: "" });
});

test("rejects inner whitespace, comma, hash", () => {
  for (const bad of ["a b", "a\tb", "a,b", "a#b", "#a"]) {
    const r = normalizeTag(bad);
    assert.equal(r.ok, false, bad);
    if (!r.ok) assert.match(r.error, /^invalid tag /);
  }
});

test("length limit counts runes, not UTF-16 units", () => {
  assert.equal(normalizeTag("a".repeat(30)).ok, true);
  assert.equal(normalizeTag("a".repeat(31)).ok, false);
  // 30 astral code points = 60 UTF-16 units — still within the limit
  assert.equal(normalizeTag("😀".repeat(30)).ok, true);
  assert.equal(normalizeTag("😀".repeat(31)).ok, false);
});

test("filter key is stable", () => {
  assert.equal(TAG_FILTER_KEY, "taskman-tagfilter");
});
