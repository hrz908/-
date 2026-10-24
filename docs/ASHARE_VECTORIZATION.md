# A股文本语料向量化系统

面向「投研问答」的 A 股文本入库方案：只向量化有信息密度的文字，每个向量块携带完整
metadata，全流程可在自有服务器离线运行（数据源走公开接口，模型本地加载）。

> 股票清单/行业/概念等结构化主数据不进向量库，见 [`ASHARE_BUSINESS_SCHEMA.md`](ASHARE_BUSINESS_SCHEMA.md)。

## 1. 边界：哪些进、哪些不进

| 类型 | 是否向量化 | 理由 |
|---|---|---|
| 年报、半年报、季报 | 是，但剔除三大报表 | 管理层讨论、风险因素、会计政策是高价值文本；报表是数字矩阵，答案应走财务取数工具 |
| 公司公告 | 是（重要公告抓正文，其余跳过） | 回购/减持/重组/业绩预告等事件驱动信息；只有标题的程序性公告信息密度太低，不入库 |
| 券商研究报告 | 是（默认抓 PDF 正文） | 机构观点、盈利预测逻辑、行业拆解；只有标题的研报不入库 |
| 行业政策 | 是（国务院政策文件库原文 + 快讯按机构+动作识别） | 政策要引用条款，不能只靠券商解读，见 §3.6 |
| 财联社电报、东财快讯 | 是 | 分钟级事件，需高频轮询 + 指纹去重 |
| 个股新闻 | 可选 | 需显式给代码；全市场扫描成本高，默认关闭 |
| K 线、资产负债表、利润表、现金流量表、实时行情 | **否** | 结构化数值，向量检索既不准也浪费存储；由 `agent/tools/*` 与 Go 网关实时取数 |
| 扫描版 PDF、图片公告 | 否 | 无文字层，OCR 成本与收益不匹配；直接跳过并记日志 |

> baostock 在本系统里不参与文本采集。它只有行情/财务数据，属于「不向量化」那一侧。

## 2. 架构

```
              免费公开接口                        离线服务器
┌──────────────────────────┐        ┌──────────────────────────────────────┐
│ 巨潮 hisAnnouncement     │        │ ashare collect                       │
│ 东财 reportapi           │──HTTP─▶│  取正文 → 清洗 → 质量门禁 → 去重      │
│ 财联社 / 同花顺 / 东财快讯 │        │  → 中文分块 → 本地 embedding → 写入    │
│ 国务院政策文件库          │        │                                      │
└──────────────────────────┘        │  PostgreSQL + pgvector               │
                                    │   documents / chunks（原有检索面）    │
                                    │   ashare_documents（富元数据）        │
                                    │   ashare_sync_state（增量水位）       │
                                    └──────────────────────────────────────┘
```

关键取舍：**不新建第二套向量表**。向量只存在 `chunks.embedding` 一份，`agent/rag/retriever.py`
沿用同一套混合检索（向量 + 全文 RRF + 重排），只补了代码/类型/时间过滤与按文档限流；
A 股特有字段落在 `ashare_documents`，完整 metadata 另存一份到 `chunks.metadata`，
满足「每个向量自带全部字段」。

代码位置：`ingest/ashare/`；迁移：`server/migrations/007_ashare_corpus.sql`。

## 3. 数据源

| 源 | 接口 | 频率 | 单次规模 | 备注 |
|---|---|---|---|---|
| `cninfo` | `POST www.cninfo.com.cn/new/hisAnnouncement/query` | 30 min | 全市场，30 条/页 | 直接返回 `adjunctUrl`，拼 `static.cninfo.com.cn` 即 PDF；公告 + 定期报告一个源全包 |
| `em-report` | `GET reportapi.eastmoney.com/report/list` | 2 h | 50 条/页 × 4 类 | `qType`：0 个股 / 1 行业 / 2 策略 / 3 宏观；个股/行业研报 PDF 直链 `pdf.dfcfw.com/pdf/H3_{infoCode}_1.pdf` |
| `cls` | `ak.stock_info_global_cls` | 5 min | 最近 20 条 | 无单条 URL；只能靠轮询 + `source_id` 去重 |
| `ths` | `ak.stock_info_global_ths` | 5 min | 最新数十条 | 同花顺 7×24，带原文链接；接口不存在时有明确报错 |
| `em-global` | `ak.stock_info_global_em` | 10 min | 最新若干条 | 市场级快讯，带链接 |
| `em-news` | `ak.stock_news_em(symbol=code)` | 1 h（opt-in） | 单只约 100 条 | 全市场 5000 只、2 QPS 约 40 分钟；默认不跑 |
| `gov-policy` | `sousuo.www.gov.cn/search-gov/data` | 1 h | 20 条/页，gw + bm | 国务院文件 + 部门文件；列表摘要太短，默认再抓详情页正文 |

### 3.1 限速与重试

`ashare/net.py` 按**域名**限速（cninfo / 政策库 1 QPS，东财 2 QPS），429/5xx 指数退避重试 3 次，
其余 4xx 直接失败。并发不放在采集层——这些接口对并发最敏感，串行 + 限速比多线程更稳。

### 3.2 增量水位

`ashare_sync_state` 每源一行：

- 窗口型（cninfo、em-report）：水位推进到查询区间右端，下次从 `水位 - 2h` 开始（研报 72h，因为发布日期可能回填），覆盖迟到公告；
- 最新型（cls、em-global、em-news）：水位取本批**处理成功**条目的最大发布时间，下次从 `水位 - 6h` 开始；
- 有失败时：小比例失败照常推进（靠重叠窗口重试），失败 ≥ max(3, 20%) 或源级异常才停水位。
  永久卡水位比偶发漏一条更糟；
- 注意重叠量不是回补窗口：daemon 每 30 分钟跑一次，重叠 24h 等于每次重扫一整天。
  历史回补一律用 `--since` 显式指定。

幂等由三层保证，重跑不会产生重复向量：`documents.source_path` 唯一 → `ashare_documents(source, source_id)` 唯一 → 内容指纹唯一。

### 3.3 公告正文策略

- 定期报告（年报/半年报/季报）：必抓 PDF，这是正文的唯一来源；
- 重要公告：标题命中回购/增减持/重组/收购/定增/业绩预告/重大合同/诉讼/处罚/问询/退市等
  白名单才抓 PDF，`ASHARE_ANNOUNCEMENT_PDF_MODE=whitelist`；
- 其余公告：没有正文，按 `empty_body` 跳过。标题级条目对问答贡献极低，宁可少入库；
  需要放宽时把模式改成 `all`（成本是 PDF 解析量成倍上涨）；
- 单次运行的 PDF 解析预算 `ASHARE_PDF_MAX_PER_RUN=300`（定期报告不占额度），避免一次回补把 CPU 打满。

### 3.4 研报正文

默认抓 PDF（`ASHARE_FETCH_REPORT_PDF=true`）。只入库标题+评级的研报对问答几乎没有价值，
所以质量门禁会直接丢掉无正文的研报。回补历史时用 `--limit` 分批，或临时关掉 PDF 只补元数据。

### 3.5 已知接口风险

- 东财 `qType` 编号随页面改版可能调整，`config.REPORT_QTYPES` 可改；字段做了 `orgSName/orgName` 之类多候选映射；
- 巨潮 `column` 默认 `szse`（沪深京），若只返回深市，改成 `sse`/`bj` 分市场跑，`ASHARE_CNINFO_COLUMN` 控制；
- akshare 列名跨版本变动，取数一律走 `util.cell()` 的多候选列名；
- 公开接口随时可能加验证码/封 IP：限速是第一道保险，`spool` 双机模式是第二道（见 §8）。

### 3.6 政策原文

两条来源并存：

1. `gov-policy`：国务院政策文件库（`gw` 国务院文件 + `bm` 部门文件），按发布时间倒序翻页，
   `source_type=industry_policy`，`org` 取发文机关，`extra` 存发文字号/主题分类/索引号。
   列表页只给一两句摘要，正文另抓详情页 `div#UCAP-CONTENT`；
2. 快讯识别：财联社/同花顺/东财快讯里标题同时命中「发文机构 + 政策动作」（如「工信部 印发」）
   才改判为政策并记录 `extra.policy_org`，作为政策发布后的市场解读补充。

政府网详情页有未闭合标签，lxml 会把整块正文丢掉，正文解析必须用 `html.parser`——
这是实测踩过的坑，别为了速度换回 lxml。

## 4. 清洗

顺序固定为：去版面噪声 → 剔报表章节 → 过滤数值行 → 切模板尾巴 → 二次归一化。

| 步骤 | 处理内容 | 目的 |
|---|---|---|
| HTML 去噪 | script/style/nav/footer、标签 | 快讯正文常混 HTML |
| 版面清理 | 页码、`第 X 页/共 Y 页`、重复 ≥5 次的短行、水印、英文断词换行 | PDF 抽字残留 |
| 报表剔除 | 从「合并/母公司+资产负债表/利润表/现金流量表/所有者权益变动表」标题起，删到下一个章节标题或叙述性长句 | 报表数字不向量化 |
| 数值行过滤 | 数字占比 > 50%，或表格行数字占比 > 35% | 留下「分产品收入构成」这类带文字的表格 |
| 模板尾巴 | 后半段出现免责声明/分析师承诺/评级说明即截断 | 研报尾部常占三成篇幅 |
| 签名块 | 末尾的「特此公告」「XX 董事会」「YYYY 年 M 月 D 日」 | 公告固定尾巴 |
| 归一化 | NFKC、全角空格、汉字间空格、连续空行 | 统一指纹与分词 |

不会做的清洗：不改写、不摘要、不翻译。原文是审计与引用依据，改写会引入不可追溯的事实偏差。

## 5. 质量门禁与去重

### 5.1 质量分

```
score = trust × (0.4×长度分 + 0.3×中文占比分 + 0.2×模板句惩罚 + 0.1×数字占比惩罚)
```

- 各类型有最小长度（快讯 30、公告 60、研报 120、年报 500 字）与目标长度（达到即满分）；
- 中文占比 < 25% 记原因；模板句行占比 > 50%、数字占比 > 40% 直接拒绝；
- `trust`：巨潮 1.0、东财研报 0.9、财联社 0.85、东财快讯/个股新闻 0.8；
- 阈值 `ASHARE_MIN_QUALITY=0.45`，被拒原因写 DEBUG 日志，便于按源调参。

### 5.2 三层去重

| 层 | 键 | 抓什么 | 代价 |
|---|---|---|---|
| 幂等 | `source_path = ashare://{source}/{source_id}` | 同一源重复采集 | 零 |
| 精确跨源 | `fingerprint = sha256(标题+代码+日期+正文)`，去标点空白 | 同一公告被两家源收录 | 一次索引查询 |
| 近重复 | 64bit SimHash，4×16bit 分段 GIN 召回候选，汉明 ≤3 且长度比 0.75~1.33 | 快讯转载、公告微调 | 一次候选查询 |

指纹必须带日期：同一公司每月发《回购进展公告》，模板几乎一致，不带日期会把后续月份全部误杀。
近重复只在同 `source_type` 内比较，避免跨类型误判。

## 6. 分块

| 参数 | 默认 | 说明 |
|---|---|---|
| `ASHARE_CHUNK_MAX_CHARS` | 450 | 中文 450 字 ≈ 350~450 token，适配 512~1024 上下文 |
| `ASHARE_CHUNK_MIN_CHARS` | 60 | 过短块并入前块，避免「特此公告。」单独成向量 |
| `ASHARE_CHUNK_OVERLAP_CHARS` | 80 | 只在超长段落硬切时生效；段落边界本身就是语义边界，不再人为重叠 |

- 先按 Markdown 标题 / 「第 X 节」切 section，再按空行切段，段落装满为止；
- 单段超长按句末标点（。！？；）切，单句仍超长才硬切并保留一句重叠；
- 每块头部注入上下文：`【贵州茅台(600519) · 公司公告 · 2026-10-24 · 一、回购进展】`。
  公告大量出现「本次回购价格不超过…」的无主语句，没有这行头，检索和生成都会失焦；
- 不用 cl100k token 计数：中文近一字一 token，绕一圈还是字符预算，反而会把数字串切碎。

## 7. Metadata 设计

### 7.1 文档级（`ashare_documents`）

| 字段 | 类型 | 示例 | 用途 |
|---|---|---|---|
| `doc_id` | UUID | — | 关联 `documents.id` |
| `source` | TEXT | `cninfo` | 采集器 |
| `source_id` | TEXT | `1221384732` | 源侧主键，幂等 |
| `source_type` | TEXT | `annual_report` | 检索过滤主键 |
| `title` | TEXT | `2025年年度报告` | 展示/全文检索 |
| `url` | TEXT | `https://static.cninfo.com.cn/...PDF` | 溯源 |
| `pub_time` | TIMESTAMPTZ | `2026-04-30 00:00+08` | 时间过滤 |
| `stock_codes` | TEXT[] | `{600519}` | 按股票过滤（GIN） |
| `stock_names` | TEXT[] | `{贵州茅台}` | 展示 |
| `industry` | TEXT | `白酒` | 行业过滤（研报） |
| `org` / `author` | TEXT | `中信证券` / `张三` | 机构、分析师 |
| `rating` | TEXT | `买入` | 东财评级 |
| `event_type` | TEXT | `股份回购` | 事件类型 |
| `quality_score` | REAL | `0.86` | 排序/审计 |
| `fingerprint` | TEXT | sha256 | 跨源去重 |
| `simhash` + `simhash_bands` | BIGINT + INT[] | — | 近重复 |
| `content_bytes` | INT | `18342` | 长度比校验 |
| `source_hash` | TEXT | sha1 | 未变早退，跳过 PDF 解析 |
| `extra` | JSONB | `{"column_id":"szse"}` / `{"pcode":"国办发〔2026〕29号"}` | 源特有字段 |

### 7.2 向量级（`chunks.metadata`，每个向量一份完整副本）

```json
{
  "source": "cninfo",
  "source_id": "1221384732",
  "source_type": "announcement",
  "title": "关于回购公司股份的进展公告",
  "url": "https://static.cninfo.com.cn/finalpage/2026-10-24/1221384732.PDF",
  "pub_time": "2026-10-24T00:00:00+08:00",
  "pub_date": "2026-10-24",
  "stock_codes": ["600519"],
  "stock_names": ["贵州茅台"],
  "industry": "",
  "org": "",
  "author": "",
  "rating": "",
  "event_type": "股份回购",
  "section": "一、回购进展",
  "chunk_seq": 1,
  "chunk_total": 3,
  "quality_score": 0.86,
  "fingerprint": "…",
  "fetched_at": "2026-10-24T09:31:02+08:00",
  "extra": {"column_id": "szse"},
  "doc_id": "9f1c…"
}
```

冗余是刻意的：检索时只查 `chunks` 就能拿到全部字段，不必回表；JSONB 的重复 key
会被 TOAST 压缩，实际开销远小于字面量。需要范围查询（时间/代码）时 join `ashare_documents`，
需要单条过滤时 `chunks.metadata @> '{"stock_codes":["600519"]}'` 走 GIN。

## 8. 向量模型与硬件

| 模型 | 原生维度 | 资源 | 中文效果 | 适用 |
|---|---|---|---|---|
| `Qwen/Qwen3-Embedding-4B`（当前默认） | 2560（MRL 可降） | fp16 权重 ~9GB | 强 | 有 GPU；顺带与 agent 在线查询保持同一空间 |
| `Qwen/Qwen3-Embedding-0.6B` | 1024 | fp32 ~2.5GB | 中上 | 纯 CPU 服务器的性价比选择 |
| `BAAI/bge-m3` | 1024 | ~2.2GB | 强，长文友好 | 需改维度；支持稀疏/多向量（本方案只用 dense） |

现状是 **1536 维**（`vector(1536)` + HNSW 上限 2000 的折中），agent 在线查询也用
`Qwen3-Embedding-4B + dimensions=1536`。三处必须一致：`ASHARE_EMBED_DIM`、
`chunks.embedding` 列、agent 的 `EMBED_DIM`。

- 本地加载时如果原生维度 > 目标维度，按 MRL 截断并**重新归一化**（`ASHARE_EMBED_TRUNCATE=true`）；
  低于目标维度直接报错，不猜；
- CPU 服务器想换 0.6B：必须一次性改维度（`vector(1024)`）+ 清空重灌 + 重建 HNSW + 同步改 agent，
  这是一次迁移而不是一个开关。没有迁移窗口就继续用 4B，把入库放在夜间批跑；
- 吞吐量级：8 vCPU 上 4B fp32 约 1~5 chunk/s，单日 7~15 万 chunk 需要数小时；
  有 GPU 时按 50~200 chunk/s 规划。入库与在线查询抢资源时，把 `ashare` 限到 1~2 核，
  或给 embedding 单独起 TEI/vLLM 服务走 `ASHARE_EMBED_API_BASE`。

单块成本估算：1536 维 float4 = 6KB，正文 ~1.4KB，metadata ~1KB，HNSW 索引 ~1~2KB，
合计 **8~12KB/块**。1000 万块约 80~120GB。省空间可换 `halfvec(1536)`（向量减半），
或降维到 1024。

## 9. 存储与检索

- 写入：一份文档一个事务（`documents` + `chunks` + `ashare_documents` 同进同退），
  `synchronous_commit=off` 提速批量入库，崩了重跑有幂等兜底；
- 索引：`chunks_hnsw`（cosine）+ `chunks_tsv` GIN + `chunks_meta` GIN + `ashare_docs_codes` GIN；
- 检索：沿用 `HybridRetriever` 的向量 + 全文 RRF + 交叉编码器重排。A 股语料接入后已补两件事：
  1. 检索支持 `stock_code` / `source_type` / `since` 过滤，条件下推到向量与全文两路召回
     （`_build_search_sql`），`search_knowledge_base` 工具已暴露这三个可选参数；
  2. 上下文限流从「同标题 2 块」改为「同文档 `RAG_PER_DOC`（默认 3）块」，
     按 `documents.id` 计数，年报/公告不再被标题重复误伤。
- 过滤审慎度：HNSW 是近似索引，过滤条件很窄时可能凑不满 `LIMIT`。pgvector 0.8+ 可开
  `SET hnsw.iterative_scan = relaxed_order`；旧版本把 `hnsw.ef_search` 提到 100~200 再截断。
- 中文全文检索是弱项：`tsv` 用 `simple` 配置，中文分词不准。向量召回是主力，全文只做补充；
  要提升可装 `zhparser`/`pg_jieba` 并改生成列，属于独立迁移。

检索自检（不经过 agent）：

```sql
SET LOCAL hnsw.ef_search = 120;
SELECT c.content, d.title, a.pub_time, 1 - (c.embedding <=> :q) AS sim
FROM chunks c
JOIN documents d ON d.id = c.document_id
JOIN ashare_documents a ON a.doc_id = d.id
WHERE a.stock_codes && ARRAY['600519']::text[]
  AND a.pub_time >= now() - interval '365 days'
ORDER BY c.embedding <=> :q
LIMIT 20;
```

## 10. 离线部署

### 10.1 模型与依赖预置

有网机器：

```bash
# 模型权重（约 8~9GB）
huggingface-cli download Qwen/Qwen3-Embedding-4B --local-dir models/Qwen3-Embedding-4B
# 依赖 wheel（可选，容器内构建更省事）
pip download -r ingest/requirements.txt -r ingest/requirements-embed.txt -d wheelhouse
```

服务器（无外网）：

```bash
# 方式 A：有网机器构建镜像后导过去（推荐，避免服务器装 torch）
docker build -t stock-agent-ingest:ashare --build-arg INSTALL_EMBED=1 ingest/
docker save stock-agent-ingest:ashare | gzip > ingest-ashare.tar.gz
# 服务器
gunzip -c ingest-ashare.tar.gz | docker load
```

把模型放到 `/opt/stock-agent/models/Qwen3-Embedding-4B`，compose 以只读挂载到 `/models`，
容器内 `HF_HUB_OFFLINE=1`/`TRANSFORMERS_OFFLINE=1`，不会尝试联网。

### 10.2 启动

```bash
cd /opt/stock-agent/deploy
# 1) 首次：执行迁移（已有库不能靠 initdb，必须手动跑）
docker compose -f docker-compose.prod.yml --profile ashare run --rm ashare python -m ashare.cli init-db
# 2) 起常驻采集
docker compose -f docker-compose.prod.yml --profile ashare up -d --build ashare
# 3) 回补历史（按需，分批）
docker compose -f docker-compose.prod.yml exec ashare python -m ashare.cli collect --source cninfo --since 2025-01-01 --limit 2000
docker compose -f docker-compose.prod.yml exec ashare python -m ashare.cli collect --source em-report --since 2026-01-01
```

### 10.3 完全隔离内网的双机模式

采集机（能出网、有库）只清洗不嵌入：

```bash
python -m ashare.cli collect --source cninfo --since 2026-09-01 --spool /data/spool
```

把 `/data/spool/*.jsonl` 拷到内网机器后：

```bash
python -m ashare.cli ingest-spool --path /data/spool/cninfo-20261024-090000.jsonl
```

spool 里存的是**清洗后的纯文本**，内网侧只做分块、嵌入、写库，不需要访问任何外部接口。

## 11. 运维手册

```bash
python -m ashare.cli stats                      # 按来源/类型统计文档数、块数、时间范围、均分
python -m ashare.cli storage                    # 表/索引实际占用 + 千万块容量评估
python -m ashare.cli search "宁德时代 海外产能" --code 300750
python -m ashare.cli collect --source cls --dry-run          # 只打印，不写库
python -m ashare.cli collect --source cninfo --force --limit 50  # 强制重跑指定条数
python -m ashare.cli reembed --limit 1000       # 换模型后按 documents.embed_model 重嵌
```

排障入口：

```sql
-- 最近运行情况
SELECT * FROM ashare_ingest_runs ORDER BY started_at DESC LIMIT 20;
-- 各源水位与错误
SELECT * FROM ashare_sync_state ORDER BY source;
-- 语料分布
SELECT * FROM ashare_corpus_stats;
```

常见问题：

| 现象 | 原因 | 处理 |
|---|---|---|
| 全部 `low_quality` | 只抓到标题，PDF 未抓/解析失败 | 看日志 `pdf_*`；确认 `ASHARE_*_PDF_*` 开关与网络 |
| `dimension mismatch` | 模型维度 ≠ `vector(N)` | 三处维度对齐；换模型走重嵌迁移 |
| 水位不动 | 连续大面积失败 | 看 `ashare_sync_state.error`；接口失效时限速/换源 |
| 检索结果重复 | 同文档多块 | 调小 `RAG_PER_DOC`，或按 `doc_id` 限流 |
| HNSW 慢/召回差 | `ef_search` 太低或过滤太窄 | 提 `ef_search`，开 iterative scan |
| 磁盘涨得快 | PDF 缓存/向量索引 | `ASHARE_KEEP_PDF=false`（默认）、定期 `VACUUM (ANALYZE)` |

备份：`pg_dump` 覆盖 `documents`/`chunks`/`ashare_*`；模型与 spool 无需备份（可重下/重跑）。
删除策略：公告撤销/更正不会自动删旧向量，当前靠「新公告覆盖语义」，需要严格版本控制时
加 `status='superseded'` 软删并在检索层过滤。

## 12. 合规与风险

- 只抓公开页面/接口，保持 1~2 QPS，不绕验证码、不伪造登录态；
- 研报全文有版权，仅用于内部检索与摘录引用，不对外批量转载；每条都保留 `url` 供溯源；
- 接口属非官方公开接口，随时可能变更；采集失败必须可见（`ashare_ingest_runs`），不能静默丢数据；
- 向量相似度不等于事实正确：生成侧仍需引用原文与发布时间，避免把旧研报当现状。
