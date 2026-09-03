package rag

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var postgresSchema string

type PostgresParentStore struct{ pool *pgxpool.Pool }

func NewPostgresParentStore(ctx context.Context, connectionString string) (*PostgresParentStore, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL URL: %w", err)
	}
	config.MaxConns = 8
	config.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return &PostgresParentStore{pool: pool}, nil
}

func (s *PostgresParentStore) EnsureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, postgresSchema)
	return err
}

func (s *PostgresParentStore) BeginDocument(ctx context.Context, input IngestInput, hash string) (Document, bool, error) {
	var existing Document
	err := s.pool.QueryRow(ctx, `SELECT id,store_id,scope,title,source_uri,media_type,version,content_hash,status,error,metadata,created_at,updated_at
		FROM rag_documents WHERE scope=$1 AND store_id=$2 AND source_uri=$3 AND content_hash=$4 AND status='ready'
		ORDER BY version DESC LIMIT 1`, input.Scope, input.StoreID, input.SourceURI, hash).Scan(
		&existing.ID, &existing.StoreID, &existing.Scope, &existing.Title, &existing.SourceURI, &existing.MediaType,
		&existing.Version, &existing.ContentHash, &existing.Status, &existing.Error, &existing.Metadata, &existing.CreatedAt, &existing.UpdatedAt)
	if err == nil {
		return existing, true, nil
	}
	if err != pgx.ErrNoRows {
		return Document{}, false, err
	}
	now := time.Now().UTC()
	doc := Document{ID: uuid.NewString(), StoreID: input.StoreID, Scope: input.Scope, Title: input.Title, SourceURI: input.SourceURI,
		MediaType: input.MediaType, ContentHash: hash, Status: StatusProcessing, Metadata: cloneMap(input.Metadata), CreatedAt: now, UpdatedAt: now}
	metadata, _ := json.Marshal(doc.Metadata)
	err = s.pool.QueryRow(ctx, `INSERT INTO rag_documents(id,store_id,scope,title,source_uri,media_type,version,content_hash,status,metadata,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,(SELECT COALESCE(MAX(version),0)+1 FROM rag_documents WHERE scope=$3 AND store_id=$2 AND source_uri=$5),$7,$8,$9,$10,$10)
		RETURNING version`, doc.ID, doc.StoreID, doc.Scope, doc.Title, doc.SourceURI, doc.MediaType, doc.ContentHash, doc.Status, metadata, now).Scan(&doc.Version)
	return doc, false, err
}

func (s *PostgresParentStore) SaveParents(ctx context.Context, doc Document, chunks []Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, chunk := range chunks {
		metadata, _ := json.Marshal(chunk.Metadata)
		var parent any
		if chunk.ParentID != "" {
			parent = chunk.ParentID
		}
		_, err = tx.Exec(ctx, `INSERT INTO rag_parent_chunks(id,doc_id,store_id,scope,document_version,level,content,chunk_index,start_char,end_char,total_children,parent_id,title,metadata)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, chunk.ID, doc.ID, chunk.StoreID, chunk.Scope, chunk.DocumentVersion,
			chunk.Level, chunk.Content, chunk.ChunkIndex, chunk.StartChar, chunk.EndChar, chunk.TotalChildren, parent, chunk.Title, metadata)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresParentStore) MarkDocument(ctx context.Context, id, status, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE rag_documents SET status=$2,error=$3,updated_at=now() WHERE id=$1`, id, status, message)
	return err
}

func (s *PostgresParentStore) ReadyDocuments(ctx context.Context, storeID string, ids []string) (map[string]bool, error) {
	result := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text FROM rag_documents d WHERE d.id::text=ANY($1::text[]) AND d.status='ready'
		AND (d.scope='platform' OR (d.scope='store' AND d.store_id=$2))
		AND NOT EXISTS (SELECT 1 FROM rag_documents newer WHERE newer.scope=d.scope AND newer.store_id=d.store_id
			AND newer.source_uri=d.source_uri AND newer.status='ready' AND newer.version>d.version)`, ids, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (s *PostgresParentStore) GetParent(ctx context.Context, storeID, id string) (Chunk, error) {
	var chunk Chunk
	err := s.pool.QueryRow(ctx, `SELECT c.id,c.doc_id,c.store_id,c.scope,c.document_version,c.level,c.content,c.chunk_index,c.start_char,c.end_char,c.total_children,
		COALESCE(c.parent_id::text,''),c.title,c.metadata FROM rag_parent_chunks c JOIN rag_documents d ON d.id=c.doc_id
		WHERE c.id=$1 AND d.status='ready' AND (c.scope='platform' OR (c.scope='store' AND c.store_id=$2))
		AND NOT EXISTS (SELECT 1 FROM rag_documents newer WHERE newer.scope=d.scope AND newer.store_id=d.store_id
			AND newer.source_uri=d.source_uri AND newer.status='ready' AND newer.version>d.version)`, id, storeID).Scan(
		&chunk.ID, &chunk.DocID, &chunk.StoreID, &chunk.Scope, &chunk.DocumentVersion, &chunk.Level, &chunk.Content, &chunk.ChunkIndex,
		&chunk.StartChar, &chunk.EndChar, &chunk.TotalChildren, &chunk.ParentID, &chunk.Title, &chunk.Metadata)
	if err != nil {
		return Chunk{}, err
	}
	if chunk.Level == 2 {
		chunk.ParentL1ID = chunk.ParentID
	}
	return chunk, nil
}

func (s *PostgresParentStore) Close() { s.pool.Close() }
