// v10 tag filter (docs/DESIGN_V10_TAGS.md "UI"). One GLOBAL selected-tag set
// per browser, persisted in localStorage (TAG_FILTER_KEY) and shared by
// every view through useTagFilter() — switching tabs keeps the filter.
// Filtering itself is server-side: each view passes `tags` into its list
// fetch and adds it to its reload effect deps.
//
// <TagFilter/> renders the known tags (GET /api/tags — open tasks only,
// caller-scoped, optionally team-narrowed) as toggle pills; selected tags
// the server no longer knows are still shown active so a stale filter can
// always be cleared. Renders nothing when there are no known tags and
// nothing is selected, or when the fetch fails (old binary without
// /api/tags).
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import type { TagCount } from "../api";
import { api } from "../api";
import { normalizeTag, TAG_FILTER_KEY } from "../tags";
import { useTasksChangedSubscription } from "../tasksChanged";

function readStored(): string[] {
  try {
    const raw = localStorage.getItem(TAG_FILTER_KEY);
    if (!raw) return [];
    const v: unknown = JSON.parse(raw);
    if (!Array.isArray(v)) return [];
    // Hygiene: normalise like the server, drop invalid/empty, dedupe — a
    // hand-edited or pre-normalisation value must not 400 every list fetch.
    const out: string[] = [];
    for (const x of v) {
      if (typeof x !== "string") continue;
      const n = normalizeTag(x);
      if (n.ok && n.tag !== "" && !out.includes(n.tag)) out.push(n.tag);
    }
    return out;
  } catch {
    return [];
  }
}

// Module-level store (same listener-set pattern as tasksChanged.ts). The
// snapshot array is replaced, never mutated, so useSyncExternalStore sees a
// stable reference between changes.
let selected: string[] = readStored();
const listeners = new Set<() => void>();

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

function getSnapshot(): string[] {
  return selected;
}

function setSelected(next: string[]): void {
  selected = next;
  try {
    if (next.length) localStorage.setItem(TAG_FILTER_KEY, JSON.stringify(next));
    else localStorage.removeItem(TAG_FILTER_KEY);
  } catch {
    // localStorage unavailable — the filter still works for this session
  }
  listeners.forEach((l) => l());
}

/** `[tags, setTags]` — the global tag filter (AND semantics server-side). */
// oxlint-disable-next-line react/only-export-components -- contract: hook and bar live in one file
export function useTagFilter(): [string[], (next: string[]) => void] {
  const tags = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return [tags, setSelected];
}

interface TagFilterProps {
  /** Narrows the known-tags list to one team (TeamPage). */
  teamId?: string;
  /** Known-tags source: open tasks (default) or done/cancelled (History). */
  status?: "open" | "closed";
}

export default function TagFilter({ teamId, status }: TagFilterProps) {
  const [tags, setTags] = useTagFilter();
  const [known, setKnown] = useState<TagCount[] | null>(null);
  const [failed, setFailed] = useState(false);

  const load = useCallback(() => {
    api
      .tags(teamId, status)
      .then((r) => {
        setKnown(r?.tags ?? []);
        setFailed(false);
      })
      .catch(() => setFailed(true));
  }, [teamId, status]);

  useEffect(() => {
    load();
  }, [load]);
  useTasksChangedSubscription(load);

  if (failed || known === null) return null;
  if (known.length === 0 && tags.length === 0) return null;

  const knownNames = new Set(known.map((k) => k.tag));
  const stale = tags.filter((t) => !knownNames.has(t));

  function toggle(tag: string) {
    setTags(tags.includes(tag) ? tags.filter((t) => t !== tag) : [...tags, tag]);
  }

  return (
    <div className="tag-filter" role="group" aria-label="Filter by tag">
      <span className="tag-filter-label">Tags</span>
      {known.map((k) => {
        const active = tags.includes(k.tag);
        return (
          <button
            key={k.tag}
            type="button"
            className={`tag-pill${active ? " active" : ""}`}
            aria-pressed={active}
            data-tag={k.tag}
            onClick={() => toggle(k.tag)}
          >
            #{k.tag}
            <span className="tag-pill-count">{k.count}</span>
          </button>
        );
      })}
      {stale.map((t) => (
        <button
          key={t}
          type="button"
          className="tag-pill active"
          aria-pressed={true}
          data-tag={t}
          title={`No ${status === "closed" ? "closed" : "open"} tasks carry this tag — click to remove it from the filter`}
          onClick={() => toggle(t)}
        >
          #{t}
          <span className="tag-pill-count">0</span>
        </button>
      ))}
      {tags.length > 0 && (
        <button type="button" className="tag-filter-clear" onClick={() => setTags([])}>
          clear
        </button>
      )}
    </div>
  );
}
