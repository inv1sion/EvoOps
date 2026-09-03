# 分层混合 RAG

EvoOps 提供两种检索后端：默认 `local` 用于离线测试与 Harness 可复现回放；`external` 是真实服务链路，复用 MedQA-Agentic-RAG 的分块参数，并接入 DashScope、Milvus、PostgreSQL 与 Redis。两种后端实现同一个 `SearchKnowledge` 契约，因此 Eino 的 `search_operations_knowledge` Tool 不需要感知存储差异。

## 入库链路

```text
PDF/TXT/Markdown/JSONL
  → 文本解析与大小/编码校验
  → L1 1200/240
  → L2 600/120
  → L3 300/60
  → text-embedding-v3（1024 维）
  → L3: Milvus dense_vector + 内置 BM25 sparse_vector
  → L1/L2 + 文档版本/状态: PostgreSQL
```

三个层级均为 Unicode 字符级滑动窗口。L3 是检索叶子块；L1/L2 保存完整上下文。块 ID 由文档 UUID、版本、层级和字符位置稳定生成。文档先处于 `processing`，只有父块和叶子块均写入成功后才切换为 `ready`；失败版本和同一来源的旧版本不会进入查询结果。

支持的输入为 PDF、UTF-8 TXT、Markdown 和 JSONL。JSONL 优先读取 `text`/`content`，也支持 `question` + `answer`。当前 PDF 路径提取文本层，不执行 OCR。

## 查询链路

```text
query → text-embedding-v3
      ├─ Milvus HNSW + COSINE 稠密召回
      └─ Milvus 中文分析器 + BM25 稀疏召回
             → 加权 RRF
             → qwen3-rerank
             → L3→L2→L1 Auto-merging
             → TopK + 上下文字符预算
             → Eino Tool result
```

稠密和稀疏召回默认权重为 0.55/0.45，RRF 常数为 60。BM25 查询失败时显式降级为 dense-only；Rerank 服务失败时保留 RRF 排序，两种降级原因都会写入 Retrieval Trace。父块按 `store_id + parent_id` 在 Redis 缓存 15 分钟。检索过滤器只允许平台知识或当前店铺知识，PostgreSQL 在返回父块前会再次检查文档状态与租户。

Auto-merging 至少需要命中两个子块，并达到父块子节点覆盖阈值（默认 0.5），才会将多个 L3 替换为 L2；L2 到 L1 使用相同规则。最终返回默认最多 3 个上下文，总字符预算 6000。

## 本地启动

```powershell
docker compose -f deployments/rag/docker-compose.yml up -d
$env:EVOOPS_RAG_BACKEND = "external"

& "C:\path\to\go.exe" run ./cmd/evoops ingest `
  -path data/knowledge/advertising-roi-playbook.md `
  -scope platform `
  -title "广告 ROI 诊断与受控处置手册"

& "C:\path\to\go.exe" run ./cmd/evoops serve
```

`OPENAI_API_KEY` 继续从本地 `.env` 或进程环境注入，不进入代码与 Git。`qwen3-rerank` 要求工作空间专属的 `compatible-api/v1/reranks` 地址，因此外部模式必须显式配置 `EVOOPS_RAG_RERANK_URL`；它不能直接复用公共的 `compatible-mode/v1` Chat Completion 地址。完整变量见 `.env.example`。

## 可观测字段

每次检索记录原始/改写 Query、Embedding/Rerank 模型、dense/sparse/RRF/最终排序、合并父块、Redis 命中、降级原因、上下文字符数、耗时和成本单位。这些字段继续进入现有 Agent Step 轨迹，并可被 Evaluation Harness 读取。
