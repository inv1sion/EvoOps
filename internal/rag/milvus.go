package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/inv1sion/evoops/internal/domain"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

type MilvusLeafStore struct {
	client     *milvusclient.Client
	collection string
	dimensions int
}

func NewMilvusLeafStore(ctx context.Context, address, token, database, collectionName string, dimensions int) (*MilvusLeafStore, error) {
	client, err := milvusclient.New(ctx, &milvusclient.ClientConfig{Address: address, APIKey: token, DBName: database})
	if err != nil {
		return nil, fmt.Errorf("connect Milvus: %w", err)
	}
	if collectionName == "" {
		collectionName = "evoops_knowledge_l3"
	}
	if dimensions <= 0 {
		dimensions = 1024
	}
	return &MilvusLeafStore{client: client, collection: collectionName, dimensions: dimensions}, nil
}

func (s *MilvusLeafStore) EnsureSchema(ctx context.Context) error {
	exists, err := s.client.HasCollection(ctx, milvusclient.NewHasCollectionOption(s.collection))
	if err != nil {
		return err
	}
	if !exists {
		schema := entity.NewSchema().
			WithField(entity.NewField().WithName("chunk_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64).WithIsPrimaryKey(true)).
			WithField(entity.NewField().WithName("doc_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64)).
			WithField(entity.NewField().WithName("store_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(256)).
			WithField(entity.NewField().WithName("scope").WithDataType(entity.FieldTypeVarChar).WithMaxLength(16)).
			WithField(entity.NewField().WithName("document_version").WithDataType(entity.FieldTypeInt64)).
			WithField(entity.NewField().WithName("parent_l1_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64)).
			WithField(entity.NewField().WithName("parent_l2_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64)).
			WithField(entity.NewField().WithName("chunk_index").WithDataType(entity.FieldTypeInt64)).
			WithField(entity.NewField().WithName("title").WithDataType(entity.FieldTypeVarChar).WithMaxLength(1024)).
			WithField(entity.NewField().WithName("text").WithDataType(entity.FieldTypeVarChar).WithMaxLength(4096).WithEnableAnalyzer(true).WithAnalyzerParams(map[string]any{"type": "chinese"})).
			WithField(entity.NewField().WithName("metadata").WithDataType(entity.FieldTypeJSON)).
			WithField(entity.NewField().WithName("dense_vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(s.dimensions))).
			WithField(entity.NewField().WithName("sparse_vector").WithDataType(entity.FieldTypeSparseVector)).
			WithFunction(entity.NewFunction().WithName("text_bm25").WithType(entity.FunctionTypeBM25).WithInputFields("text").WithOutputFields("sparse_vector"))
		indexes := []milvusclient.CreateIndexOption{
			milvusclient.NewCreateIndexOption(s.collection, "dense_vector", index.NewHNSWIndex(entity.COSINE, 16, 200)),
			milvusclient.NewCreateIndexOption(s.collection, "sparse_vector", index.NewSparseInvertedIndex(entity.BM25, 0.2)),
		}
		if err := s.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(s.collection, schema).WithIndexOptions(indexes...)); err != nil {
			return err
		}
	}
	task, err := s.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(s.collection))
	if err != nil {
		return err
	}
	return task.Await(ctx)
}

func (s *MilvusLeafStore) Upsert(ctx context.Context, records []LeafRecord) error {
	if len(records) == 0 {
		return nil
	}
	ids, docs, stores, scopes := make([]string, len(records)), make([]string, len(records)), make([]string, len(records)), make([]string, len(records))
	versions, indexes := make([]int64, len(records)), make([]int64, len(records))
	l1, l2, titles, texts := make([]string, len(records)), make([]string, len(records)), make([]string, len(records)), make([]string, len(records))
	metadata, vectors := make([][]byte, len(records)), make([][]float32, len(records))
	for i, record := range records {
		if len(record.Dense) != s.dimensions {
			return fmt.Errorf("chunk %s embedding dimension %d does not match %d", record.Chunk.ID, len(record.Dense), s.dimensions)
		}
		ids[i], docs[i], stores[i], scopes[i] = record.Chunk.ID, record.Chunk.DocID, record.Chunk.StoreID, record.Chunk.Scope
		versions[i], indexes[i] = record.Chunk.DocumentVersion, int64(record.Chunk.ChunkIndex)
		l1[i], l2[i], titles[i], texts[i], vectors[i] = record.Chunk.ParentL1ID, record.Chunk.ParentL2ID, record.Chunk.Title, record.Chunk.Content, record.Dense
		metadata[i], _ = json.Marshal(record.Chunk.Metadata)
	}
	_, err := s.client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(s.collection).
		WithVarcharColumn("chunk_id", ids).WithVarcharColumn("doc_id", docs).WithVarcharColumn("store_id", stores).WithVarcharColumn("scope", scopes).
		WithInt64Column("document_version", versions).WithVarcharColumn("parent_l1_id", l1).WithVarcharColumn("parent_l2_id", l2).
		WithInt64Column("chunk_index", indexes).WithVarcharColumn("title", titles).WithVarcharColumn("text", texts).
		WithFloatVectorColumn("dense_vector", s.dimensions, vectors).WithColumns(column.NewColumnJSONBytes("metadata", metadata)))
	return err
}

func (s *MilvusLeafStore) DeleteDocument(ctx context.Context, docID string) error {
	_, err := s.client.Delete(ctx, milvusclient.NewDeleteOption(s.collection).WithExpr("doc_id == "+quoteMilvus(docID)))
	return err
}

type milvusRow struct {
	ChunkID    string `milvus:"name:chunk_id"`
	DocID      string `milvus:"name:doc_id"`
	StoreID    string `milvus:"name:store_id"`
	Scope      string `milvus:"name:scope"`
	Version    int64  `milvus:"name:document_version"`
	ParentL1ID string `milvus:"name:parent_l1_id"`
	ParentL2ID string `milvus:"name:parent_l2_id"`
	ChunkIndex int64  `milvus:"name:chunk_index"`
	Title      string `milvus:"name:title"`
	Text       string `milvus:"name:text"`
	Metadata   []byte `milvus:"name:metadata"`
}

func (s *MilvusLeafStore) Search(ctx context.Context, storeID, query string, vector []float32, cfg domain.RetrievalConfig) (LeafSearchResult, error) {
	if len(vector) != s.dimensions {
		return LeafSearchResult{}, fmt.Errorf("query embedding dimension %d does not match %d", len(vector), s.dimensions)
	}
	limit := cfg.CandidateK
	if limit <= 0 {
		limit = 12
	}
	filter := `scope == "platform" or (scope == "store" and store_id == {store_id})`
	fields := []string{"chunk_id", "doc_id", "store_id", "scope", "document_version", "parent_l1_id", "parent_l2_id", "chunk_index", "title", "text", "metadata"}
	denseSets, err := s.client.Search(ctx, milvusclient.NewSearchOption(s.collection, limit, []entity.Vector{entity.FloatVector(vector)}).
		WithANNSField("dense_vector").WithAnnParam(index.NewHNSWAnnParam(128)).WithConsistencyLevel(entity.ClStrong).
		WithFilter(filter).WithTemplateParam("store_id", storeID).WithOutputFields(fields...))
	if err != nil {
		return LeafSearchResult{}, fmt.Errorf("Milvus dense search: %w", err)
	}
	denseHits, err := decodeMilvus(denseSets)
	if err != nil {
		return LeafSearchResult{}, err
	}
	sparseSets, sparseErr := s.client.Search(ctx, milvusclient.NewSearchOption(s.collection, limit, []entity.Vector{entity.Text(query)}).
		WithANNSField("sparse_vector").WithConsistencyLevel(entity.ClStrong).
		WithFilter(filter).WithTemplateParam("store_id", storeID).WithOutputFields(fields...))
	var sparseHits []scoredHit
	if sparseErr == nil {
		sparseHits, sparseErr = decodeMilvus(sparseSets)
	}
	return fuseMilvus(denseHits, sparseHits, cfg, sparseErr), nil
}

type scoredHit struct {
	hit   domain.RetrievalHit
	score float32
}

func decodeMilvus(sets []milvusclient.ResultSet) ([]scoredHit, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	if sets[0].Err != nil {
		return nil, sets[0].Err
	}
	var rows []*milvusRow
	if err := sets[0].Unmarshal(&rows); err != nil {
		return nil, err
	}
	if len(sets[0].Scores) != len(rows) {
		return nil, fmt.Errorf("Milvus returned %d rows but %d scores", len(rows), len(sets[0].Scores))
	}
	result := make([]scoredHit, len(rows))
	for i, row := range rows {
		var metadata map[string]any
		_ = json.Unmarshal(row.Metadata, &metadata)
		result[i] = scoredHit{score: sets[0].Scores[i], hit: domain.RetrievalHit{Chunk: domain.KnowledgeChunk{
			ID: row.ChunkID, DocID: row.DocID, ParentID: row.ParentL2ID, ParentL1ID: row.ParentL1ID, ParentL2ID: row.ParentL2ID,
			StoreID: row.StoreID, Scope: row.Scope, DocumentVersion: row.Version, Level: 3, ChunkIndex: int(row.ChunkIndex), Title: row.Title, Text: row.Text, Metadata: metadata,
		}}}
	}
	return result, nil
}

func fuseMilvus(dense, sparse []scoredHit, cfg domain.RetrievalConfig, sparseErr error) LeafSearchResult {
	byID := map[string]domain.RetrievalHit{}
	denseRank, sparseRank := make([]string, 0, len(dense)), make([]string, 0, len(sparse))
	for rank, item := range dense {
		hit := item.hit
		hit.DenseScore = round(float64(item.score))
		hit.RRFScore += cfg.DenseWeight / float64(cfg.RRFK+rank+1)
		byID[hit.Chunk.ID] = hit
		denseRank = append(denseRank, hit.Chunk.ID)
	}
	for rank, item := range sparse {
		hit := byID[item.hit.Chunk.ID]
		if hit.Chunk.ID == "" {
			hit = item.hit
		}
		hit.SparseScore = round(float64(item.score))
		hit.RRFScore += cfg.SparseWeight / float64(cfg.RRFK+rank+1)
		byID[hit.Chunk.ID] = hit
		sparseRank = append(sparseRank, hit.Chunk.ID)
	}
	hits := make([]domain.RetrievalHit, 0, len(byID))
	for _, hit := range byID {
		hit.RRFScore = round(hit.RRFScore)
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].RRFScore == hits[j].RRFScore {
			return hits[i].Chunk.ID < hits[j].Chunk.ID
		}
		return hits[i].RRFScore > hits[j].RRFScore
	})
	if len(hits) > cfg.CandidateK && cfg.CandidateK > 0 {
		hits = hits[:cfg.CandidateK]
	}
	result := LeafSearchResult{Hits: hits, DenseRanking: denseRank, SparseRanking: sparseRank}
	if sparseErr != nil {
		result.DenseFallback = true
		result.FallbackReason = sparseErr.Error()
	}
	return result
}

func quoteMilvus(value string) string { data, _ := json.Marshal(value); return string(data) }

func (s *MilvusLeafStore) Close(ctx context.Context) error { return s.client.Close(ctx) }
