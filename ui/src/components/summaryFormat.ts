// Pure client-side renderers for the v9 date-range summary panel
// (docs/DESIGN_V9_NOTES_SUMMARY.md "summaryFormat.ts"). No React, no DOM —
// this module must be loadable by plain `node --test` (see
// ui/test/summaryFormat.test.ts), which is also why it sticks to erasable
// TS syntax only (tsconfig.app.json's "erasableSyntaxOnly") and pulls in
// nothing with a runtime side effect (the `import type` below is fully
// erased, so it doesn't even require ../api.ts to resolve at runtime).
import type { ActivityEntry, SummaryResponse, SummaryTask } from "../api";

type Section = "completed" | "updated" | "added";

const SECTION_LABEL: Record<Section, string> = { completed: "Completed", updated: "Updated", added: "New" };

// ---------------------------------------------------------------------------
// CSV — UTF-8 BOM, \r\n rows, every field double-quoted ("" for an internal
// quote). One row per (task, note); a zero-note task gets one row with the
// note-* columns blank. Status entries render as raw "from → to" (matches
// the contract's own example — not a display label).
// ---------------------------------------------------------------------------
const CSV_HEADER = [
  "section",
  "task",
  "status",
  "priority",
  "horizon",
  "team",
  "assignee",
  "dueDate",
  "closedDate",
  "overdue",
  "noteDate",
  "noteBy",
  "noteKind",
  "note",
];

function csvField(value: string): string {
  return `"${value.replace(/"/g, '""')}"`;
}

function csvRow(fields: string[]): string {
  return fields.map(csvField).join(",") + "\r\n";
}

function noteText(n: ActivityEntry): string {
  return n.kind === "status" ? `${n.from} → ${n.to}` : (n.text ?? "");
}

function taskCSVBase(section: Section, t: SummaryTask): string[] {
  return [
    SECTION_LABEL[section],
    t.title,
    t.status,
    t.priority,
    t.horizon,
    t.teamName ?? "",
    t.assigneeName ?? "",
    t.dueDate ?? "",
    t.closedDate ?? "",
    t.overdue ? "true" : "false",
  ];
}

export function toCSV(s: SummaryResponse): string {
  let out = "\uFEFF" + csvRow(CSV_HEADER);
  const sections: { section: Section; tasks: SummaryTask[] }[] = [
    { section: "completed", tasks: s.completed },
    { section: "updated", tasks: s.updated },
    { section: "added", tasks: s.added },
  ];
  for (const { section, tasks } of sections) {
    for (const t of tasks) {
      const base = taskCSVBase(section, t);
      if (t.notes.length === 0) {
        out += csvRow([...base, "", "", "", ""]);
        continue;
      }
      for (const n of t.notes) {
        out += csvRow([...base, n.date, n.byName ?? "", n.kind, noteText(n)]);
      }
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// Markdown — "# Summary <from> → <to> · <scopeLabel>", then one "## Section
// (n)" block per section (task bullets, dated note sub-bullets, "_none_"
// when empty). Meta parts omit anything absent.
// ---------------------------------------------------------------------------
function taskMetaParts(t: SummaryTask, section: Section): string[] {
  const parts: string[] = [];
  if (section === "completed" && t.closedDate) parts.push(`closed ${t.closedDate}`);
  if (t.priority) parts.push(t.priority);
  if (t.dueDate) parts.push(`due ${t.dueDate}`);
  if (t.assigneeName) parts.push(t.assigneeName);
  if (t.overdue) parts.push("overdue");
  return parts;
}

function taskMarkdown(t: SummaryTask, section: Section): string {
  const meta = taskMetaParts(t, section);
  const head = meta.length ? `- **${t.title}** — ${meta.join(" · ")}` : `- **${t.title}**`;
  const noteLines = t.notes.map((n) => `  - ${n.date} · ${n.byName ?? ""}: ${noteText(n)}`);
  return [head, ...noteLines].join("\n");
}

function sectionMarkdown(section: Section, tasks: SummaryTask[]): string {
  const header = `## ${SECTION_LABEL[section]} (${tasks.length})`;
  if (tasks.length === 0) return `${header}\n_none_`;
  return [header, ...tasks.map((t) => taskMarkdown(t, section))].join("\n");
}

export function toMarkdown(s: SummaryResponse, scopeLabel: string): string {
  const lines = [
    `# Summary ${s.from} → ${s.to} · ${scopeLabel}`,
    sectionMarkdown("completed", s.completed),
    sectionMarkdown("updated", s.updated),
    sectionMarkdown("added", s.added),
  ];
  return lines.join("\n") + "\n";
}
