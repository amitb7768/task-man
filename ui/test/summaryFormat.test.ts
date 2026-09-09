// Runnable check for summaryFormat.ts (docs/DESIGN_V9_NOTES_SUMMARY.md
// "summaryFormat.ts"). Plain node:test + node:assert — summaryFormat.ts is
// pure (no React/DOM), so Node's native TS type-stripping can load it
// directly. Run via `npm test` (package.json: "test": "node --test test/").
import { test } from "node:test";
import assert from "node:assert/strict";
import { toCSV, toMarkdown } from "../src/components/summaryFormat.ts";
import type { SummaryResponse, SummaryTask } from "../src/api.ts";

function task(overrides: Partial<SummaryTask> & Pick<SummaryTask, "id" | "title">): SummaryTask {
  return {
    status: "todo",
    priority: "",
    horizon: "daily",
    createdAt: "2026-09-01T00:00:00Z",
    overdue: false,
    notes: [],
    ...overrides,
  };
}

function summary(overrides: Partial<SummaryResponse> = {}): SummaryResponse {
  return {
    from: "2026-08-31",
    to: "2026-09-06",
    completed: [],
    updated: [],
    added: [],
    ...overrides,
  };
}

test("toCSV escapes an internal quote in a task title", () => {
  const csv = toCSV(summary({ completed: [task({ id: "t1", title: 'Say "hi"', status: "done" })] }));
  assert.ok(csv.includes('"Say ""hi"""'), csv);
});

test("toCSV emits one row with blank note columns for a zero-note task", () => {
  const csv = toCSV(summary({ added: [task({ id: "t2", title: "No notes" })] }));
  const rows = csv.trim().split("\r\n");
  assert.equal(rows.length, 2); // header + 1 task row
  assert.ok(rows[1].endsWith('"","","",""'), rows[1]);
});

test("toCSV preserves a literal newline embedded in a note", () => {
  const csv = toCSV(
    summary({
      updated: [
        task({
          id: "t3",
          title: "Multiline note",
          notes: [
            { id: "n1", kind: "note", date: "2026-09-02", at: "2026-09-02T10:00:00Z", byName: "Alice", text: "line one\nline two" },
          ],
        }),
      ],
    }),
  );
  assert.ok(csv.includes('"line one\nline two"'), csv);
  // The embedded bare \n must not fragment the row: splitting on the row
  // separator (\r\n) still yields exactly header + 1 data row.
  const rows = csv.trim().split("\r\n");
  assert.equal(rows.length, 2, JSON.stringify(rows));
});

test("toCSV renders a status note as raw \"from → to\", not a display label", () => {
  const csv = toCSV(
    summary({
      completed: [
        task({
          id: "t4",
          title: "Status task",
          status: "done",
          closedDate: "2026-09-02",
          notes: [{ id: "n2", kind: "status", date: "2026-09-02", at: "2026-09-02T09:00:00Z", byName: "Bob", from: "todo", to: "done" }],
        }),
      ],
    }),
  );
  assert.ok(csv.includes('"todo → done"'), csv);
});

test("toCSV starts with a UTF-8 BOM", () => {
  const csv = toCSV(summary());
  assert.equal(csv.charCodeAt(0), 0xfeff);
});

test("toCSV emits the exact header row", () => {
  const csv = toCSV(summary());
  assert.equal(
    csv.split("\r\n")[0],
    '﻿"section","task","status","priority","horizon","team","assignee","dueDate","closedDate","overdue","noteDate","noteBy","noteKind","note"',
  );
});

test("toMarkdown prints section counts, _none_ for empty sections, and omits absent meta parts", () => {
  const md = toMarkdown(
    summary({
      completed: [task({ id: "t5", title: "Done thing", status: "done", priority: "high", closedDate: "2026-09-02", dueDate: "2026-09-01" })],
    }),
    "My tasks",
  );
  assert.match(md, /^# Summary 2026-08-31 → 2026-09-06 · My tasks$/m);
  assert.match(md, /^## Completed \(1\)$/m);
  assert.match(md, /^## Updated \(0\)$/m);
  assert.match(md, /^## New \(0\)$/m);
  assert.ok(md.includes("_none_"));
  assert.ok(md.includes("- **Done thing** — closed 2026-09-02 · high · due 2026-09-01"));
});

test("toMarkdown lists dated notes under a task as sub-bullets", () => {
  const md = toMarkdown(
    summary({
      updated: [
        task({
          id: "t6",
          title: "Has notes",
          status: "in_progress",
          notes: [
            { id: "n3", kind: "note", date: "2026-09-01", at: "2026-09-01T08:00:00Z", byName: "Alice", text: "note text" },
            { id: "n4", kind: "status", date: "2026-09-02", at: "2026-09-02T08:00:00Z", byName: "Alice", from: "todo", to: "in_progress" },
          ],
        }),
      ],
    }),
    "Team X",
  );
  assert.ok(md.includes("  - 2026-09-01 · Alice: note text"));
  assert.ok(md.includes("  - 2026-09-02 · Alice: todo → in_progress"));
});
