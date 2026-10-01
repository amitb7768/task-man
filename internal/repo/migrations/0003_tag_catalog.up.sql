-- v11: admin-managed tag catalog (docs/DESIGN_V11_TAG_CATALOG.md). A task may
-- carry only tags named here (enforced in the service, not by FK: tasks.tags
-- stays a jsonb array). Names are stored normalised (model.NormalizeTags);
-- COLLATE "C" keeps ORDER BY name byte-stable.
CREATE TABLE tags (
    name       TEXT COLLATE "C" PRIMARY KEY,
    created_by TEXT REFERENCES members(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL
);
-- Seed from what tasks already carry so nothing becomes invalid. Idempotent.
INSERT INTO tags (name, created_at)
SELECT DISTINCT x.tag, now() FROM tasks t, jsonb_array_elements_text(t.tags) AS x(tag)
ON CONFLICT DO NOTHING;
