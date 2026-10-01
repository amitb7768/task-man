// Tags admin page (docs/DESIGN_V11_TAG_CATALOG.md "UI"). ADMIN-only nav
// entry (App.tsx filters it like Backlog; the server enforces requireAdmin
// on catalog writes regardless). Eyebrow/title come from App's header meta.
//
// Anatomy: a create input at the top (Enter submits, server message shown
// inline on 400/409), then one row per catalog tag — `#name` chip, mono
// "N open" count, and a delete button disabled while open tasks carry the
// tag (the server still 409s if closed tasks do). Counts come from
// GET /api/tags (open tasks), names/order from GET /api/tags/catalog.
// Every successful write reloads both and calls notifyTasksChanged() so
// mounted tag filter bars refresh.
import { useCallback, useEffect, useState } from "react";
import type { Tag } from "../api";
import { api, ApiError } from "../api";
import { notifyTasksChanged } from "../tasksChanged";
import "../styles/tags-page.css";

function errMsg(e: unknown): string {
  return e instanceof ApiError ? e.message : String(e);
}

export default function TagsView() {
  const [catalog, setCatalog] = useState<Tag[] | null>(null);
  const [counts, setCounts] = useState<Map<string, number>>(new Map());
  const [loadError, setLoadError] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    return Promise.all([api.tagCatalog(), api.tags()])
      .then(([cat, tc]) => {
        const sorted = [...(cat?.tags ?? [])].sort((a, b) => a.name.localeCompare(b.name));
        setCatalog(sorted);
        setCounts(new Map((tc?.tags ?? []).map((t) => [t.tag, t.count])));
        setLoadError(null);
      })
      .catch((e) => setLoadError(errMsg(e)));
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  async function create() {
    const name = draft.trim();
    if (!name || busy) return;
    setBusy(true);
    setCreateError(null);
    try {
      await api.createTag(name);
      setDraft("");
      await reload();
      notifyTasksChanged();
    } catch (e) {
      setCreateError(errMsg(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(name: string) {
    if (busy) return;
    setBusy(true);
    setListError(null);
    try {
      await api.deleteTag(name);
      await reload();
      notifyTasksChanged();
    } catch (e) {
      setListError(errMsg(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="tags-page">
      <div className="tags-create">
        <input
          className="tags-create-input"
          aria-label="New tag"
          placeholder="New tag name — press Enter to create"
          value={draft}
          disabled={busy}
          onChange={(e) => {
            setDraft(e.target.value);
            if (createError) setCreateError(null);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              create();
            }
          }}
        />
        {createError && (
          <div className="tags-create-error" role="alert">
            {createError}
          </div>
        )}
      </div>

      {loadError && <div className="error tags-load-error">{loadError}</div>}
      {listError && (
        <div className="tags-list-error" role="alert">
          {listError}
        </div>
      )}

      {catalog !== null &&
        (catalog.length === 0 ? (
          <div className="tags-empty">No tags yet. Create the first one above.</div>
        ) : (
          <ul className="tags-list">
            {catalog.map((t) => {
              const n = counts.get(t.name) ?? 0;
              return (
                <li key={t.name} className="tags-row" data-tag={t.name}>
                  <span className="chip tag tags-row-chip">{t.name}</span>
                  <span className={`tags-row-count${n === 0 ? " zero" : ""}`}>{n} open</span>
                  <button
                    type="button"
                    className="tags-delete"
                    aria-label={`Delete tag ${t.name}`}
                    title={n > 0 ? `In use by ${n} task${n === 1 ? "" : "s"}` : "Delete tag"}
                    disabled={busy || n > 0}
                    onClick={() => remove(t.name)}
                  >
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M4 7h16" />
                      <path d="M9.5 7V4.5h5V7" />
                      <path d="M6.5 7l1 12.5h9l1-12.5" />
                    </svg>
                  </button>
                </li>
              );
            })}
          </ul>
        ))}
    </div>
  );
}
