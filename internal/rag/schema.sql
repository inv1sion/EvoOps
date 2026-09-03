CREATE TABLE IF NOT EXISTS rag_documents (
    id uuid PRIMARY KEY,
    store_id text NOT NULL DEFAULT '',
    scope text NOT NULL CHECK (scope IN ('platform', 'store')),
    title text NOT NULL,
    source_uri text NOT NULL,
    media_type text NOT NULL,
    version bigint NOT NULL,
    content_hash char(64) NOT NULL,
    status text NOT NULL CHECK (status IN ('processing', 'ready', 'failed')),
    error text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (scope, store_id, source_uri, version)
);

CREATE INDEX IF NOT EXISTS rag_documents_ready_hash_idx
    ON rag_documents(scope, store_id, content_hash) WHERE status = 'ready';

CREATE TABLE IF NOT EXISTS rag_parent_chunks (
    id uuid PRIMARY KEY,
    doc_id uuid NOT NULL REFERENCES rag_documents(id) ON DELETE CASCADE,
    store_id text NOT NULL DEFAULT '',
    scope text NOT NULL CHECK (scope IN ('platform', 'store')),
    document_version bigint NOT NULL,
    level smallint NOT NULL CHECK (level IN (1, 2)),
    content text NOT NULL,
    chunk_index integer NOT NULL,
    start_char integer NOT NULL,
    end_char integer NOT NULL,
    total_children integer NOT NULL,
    parent_id uuid NULL REFERENCES rag_parent_chunks(id) ON DELETE CASCADE,
    title text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS rag_parent_chunks_doc_idx ON rag_parent_chunks(doc_id, level, chunk_index);
CREATE INDEX IF NOT EXISTS rag_parent_chunks_parent_idx ON rag_parent_chunks(parent_id);
