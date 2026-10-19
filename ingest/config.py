"""Ingest 服务全局配置。所有配置项均可通过环境变量覆盖。"""
import os

DATABASE_URL = os.getenv("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/stock_agent")
REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379/0")
WATCH_DIR = os.getenv("WATCH_DIR", "./data/docs")
EMBED_MODEL = os.getenv("EMBED_MODEL", "BAAI/bge-m3")
EMBED_DIM = int(os.getenv("EMBED_DIM", "1024"))
CHUNK_SIZE = int(os.getenv("CHUNK_SIZE", "512"))  # tokens
CHUNK_OVERLAP = int(os.getenv("CHUNK_OVERLAP", "64"))

# 支持的文件扩展名
SUPPORTED_EXTENSIONS = {".pdf", ".docx", ".pptx", ".md", ".txt", ".csv", ".xlsx"}

# ---- Embedding 降级方案（FlagEmbedding 不可用时走 OpenAI 兼容 API）----
OPENAI_API_BASE = os.getenv("OPENAI_API_BASE", "")
OPENAI_API_KEY = os.getenv("OPENAI_API_KEY", "")
OPENAI_EMBED_MODEL = os.getenv("OPENAI_EMBED_MODEL", "text-embedding-3-large")

# Redis embedding 缓存 TTL（秒），默认 7 天
EMBED_CACHE_TTL = int(os.getenv("EMBED_CACHE_TTL", str(7 * 24 * 3600)))

# 去重锁 TTL（秒）
LOCK_TTL = int(os.getenv("LOCK_TTL", "300"))

# 嵌入批大小
EMBED_BATCH_SIZE = int(os.getenv("EMBED_BATCH_SIZE", "32"))
