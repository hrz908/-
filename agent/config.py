"""全局配置：全部走环境变量，本地开发给默认值。"""
import os

DATABASE_URL = os.getenv("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/stock_agent")
REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379/0")
OPENAI_API_BASE = os.getenv("OPENAI_API_BASE", "https://api.openai.com/v1")
OPENAI_API_KEY = os.getenv("OPENAI_API_KEY", "")
LLM_MODEL = os.getenv("LLM_MODEL", "gpt-4o")
SMALL_MODEL = os.getenv("SMALL_MODEL", "gpt-4o-mini")
EMBED_MODEL = os.getenv("EMBED_MODEL", "BAAI/bge-m3")
EMBED_DIM = 1024

# 工具超时
TOOL_TIMEOUT = float(os.getenv("TOOL_TIMEOUT", "8.0"))
# LLM 单次请求超时（秒）：流式生成累计可远超此值的场景不存在——
# 该超时约束的是每次请求的完成时间；不设的话上游卡死会挂住整条事件流
LLM_TIMEOUT = float(os.getenv("LLM_TIMEOUT", "60"))
# RAG 相关度阈值：对余弦相似度设阈（不是 RRF 融合分）。
# bge-m3 的 query↔chunk 相关命中实测约 0.22~0.5，故默认 0.2；
# 语料/模型变化时需重新校准（阈值过高会让 RAG 恒 escalate 到 mcp、从不生成引用回答）
RAG_THRESHOLD = float(os.getenv("RAG_THRESHOLD", "0.2"))
# Go 网关地址（工具调用回现有后端获取行情）
GO_SERVER_URL = os.getenv("GO_SERVER_URL", "http://127.0.0.1:8080")
# 调 Go 网关受保护接口用的服务间 token（Agent 作为可信内部服务）
GO_SERVER_TOKEN = os.getenv("GO_SERVER_TOKEN", "")

# Tushare Pro：第三方只读数据增强（财务/资金流/资讯/历史K线）。
# 直接走 HTTP REST（POST http://api.tushare.pro），不引入 tushare SDK：
# 纯异步 httpx 调用，复用 call_tool_safe 的超时/重试/降级，且不给镜像增加 pandas 等重依赖。
# token 为空时相关工具不注册（见 tools.get_tools），避免 LLM 选中后必然失败。
# 注意：多数增强接口需 2000+ 积分，免费新用户（约 120 积分）通常仅 daily/adj_factor 可用
TUSHARE_TOKEN = os.getenv("TUSHARE_TOKEN", "")
TUSHARE_API = os.getenv("TUSHARE_API", "http://api.tushare.pro")

# 对话 token 超过该值触发滚动摘要
SUMMARY_TOKEN_LIMIT = int(os.getenv("SUMMARY_TOKEN_LIMIT", "6000"))

# 单次 LLM 请求允许的最大输入 token（保守上限，留足余量）。
# 上游模型上下文是 163840（DeepSeek-V3），但一旦超了上游直接返回 400：
#   'number of input tokens (301046) has exceeded max_seq_len (163840) limit'
# 这类错误冒泡会让整轮回答变成空，所以发送前先按这个预算裁剪。
MAX_INPUT_TOKENS = int(os.getenv("MAX_INPUT_TOKENS", "60000"))

# Tushare 单个接口返回的最大行数（硬上限）。
# daily / daily_basic / moneyflow 这类接口在只给 ts_code 时会返回该股全部历史
# （茅台约 6000 行），整段 JSON 进对话会直接撑爆模型上下文。
MAX_TUSHARE_ROWS = int(os.getenv("MAX_TUSHARE_ROWS", "200"))
