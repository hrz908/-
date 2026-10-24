"""Ingest 服务全局配置。所有配置项均可通过环境变量覆盖。"""
import os

DATABASE_URL = os.getenv("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/stock_agent")
REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379/0")
WATCH_DIR = os.getenv("WATCH_DIR", "./data/docs")
EMBED_MODEL = os.getenv("EMBED_MODEL", "Qwen/Qwen3-Embedding-4B")
# 输出维度：必须与 agent 端、数据库列 vector(N) 三者一致。
# Qwen3 系列支持 dimensions 降维；1536 是保住 HNSW 的上限内取值
# （pgvector 对 vector 类型的 HNSW 索引硬限 2000 维）
EMBED_DIM = int(os.getenv("EMBED_DIM", "1536"))
CHUNK_SIZE = int(os.getenv("CHUNK_SIZE", "512"))  # tokens
CHUNK_OVERLAP = int(os.getenv("CHUNK_OVERLAP", "64"))

# 支持的文件扩展名
SUPPORTED_EXTENSIONS = {".pdf", ".docx", ".pptx", ".md", ".txt", ".csv", ".xlsx"}

# ---- Embedding 降级方案（FlagEmbedding 不可用时走 OpenAI 兼容 API）----
OPENAI_API_BASE = os.getenv("OPENAI_API_BASE", "")
OPENAI_API_KEY = os.getenv("OPENAI_API_KEY", "")
OPENAI_EMBED_MODEL = os.getenv("OPENAI_EMBED_MODEL", "Qwen/Qwen3-Embedding-4B")

# Redis embedding 缓存 TTL（秒），默认 7 天
EMBED_CACHE_TTL = int(os.getenv("EMBED_CACHE_TTL", str(7 * 24 * 3600)))

# 去重锁 TTL（秒）
LOCK_TTL = int(os.getenv("LOCK_TTL", "300"))

# 嵌入批大小
EMBED_BATCH_SIZE = int(os.getenv("EMBED_BATCH_SIZE", "32"))
