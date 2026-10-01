-- v10: free-form tags on tasks (docs/DESIGN_V10_TAGS.md). A jsonb array of
-- normalised strings; never SQL NULL, never jsonb 'null' (model.Tags.Value
-- always writes an array). GIN jsonb_path_ops serves the `tags @> '[...]'`
-- containment filter every list read uses.
ALTER TABLE tasks ADD COLUMN tags JSONB NOT NULL DEFAULT '[]'::jsonb;
CREATE INDEX tasks_tags ON tasks USING GIN (tags jsonb_path_ops);
