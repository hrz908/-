"""全局配置：全部走环境变量，本地开发给默认值。"""
import os

DATABASE_URL = os.getenv("DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/stock_agent")
REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379/0")
OPENAI_API_BASE = os.getenv("OPENAI_API_BASE", "https://api.openai.com/v1")
OPENAI_API_KEY = os.getenv("OPENAI_API_KEY", "")
LLM_MODEL = os.getenv("LLM_MODEL", "gpt-4o")
SMALL_MODEL = os.getenv("SMALL_MODEL", "gpt-4o-mini")
EMBED_MODEL = os.getenv("EMBED_MODEL", "BAAI/bge-m3")

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

# ===== MCP 工具源（炒股 agent 取数工具，LangGraph 架构 2026-10-23）=====
# 取数工具可由 mcp/stock_server.py（FastMCP，stdio 子进程）暴露；
# 加载失败自动回退进程内直连（tools.get_tools），上层无感知。
# 注意 mcp python-sdk 必须钉 1.x（2.x 与 langchain-mcp-adapters 0.3.x 不兼容）
STOCK_MCP_ENABLED = os.getenv("STOCK_MCP_ENABLED", "true").strip().lower() in ("1", "true", "yes", "on")
STOCK_MCP_SERVER_PATH = os.getenv(
    "STOCK_MCP_SERVER_PATH",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "mcp", "stock_server.py"),
)

# 数据源说明（2026-10-23 起）：
# 卡片数据 = akshare（东财/新浪，免 token） + baostock（历史日 K，免 token）
# 原配置 TUSHARE_TOKEN / TUSHARE_API 已废弃删除；
# 实测原 token 对 moneyflow/top_list/top_inst 等接口无权限（40203）。
# akshare 抓东财/新浪公开页面，Docker 内直连即可；本机调试若走系统代理失败，
# 进程内设 NO_PROXY=* 绕过。

# 对话 token 超过该值触发滚动摘要
SUMMARY_TOKEN_LIMIT = int(os.getenv("SUMMARY_TOKEN_LIMIT", "6000"))

# ===== 会话历史（chat_sessions 注册表，2026-10-24）=====
# 单用户保留的会话数上限：超出自动淘汰最旧的（删注册行 + checkpoints + 摘要）。
# 限额取足，正常使用碰不到；防的是脚本/误用导致的会话数无限增长
SESSION_KEEP_MAX = int(os.getenv("SESSION_KEEP_MAX", "50"))
# 历史对话列表一次最多返回的会话数
SESSION_LIST_MAX = int(os.getenv("SESSION_LIST_MAX", "50"))

# 单次 LLM 请求允许的最大输入 token（保守上限，留足余量）。
# 上游模型上下文是 163840（DeepSeek-V3），但一旦超了上游直接返回 400：
#   'number of input tokens (301046) has exceeded max_seq_len (163840) limit'
# 这类错误冒泡会让整轮回答变成空，所以发送前先按这个预算裁剪。
MAX_INPUT_TOKENS = int(os.getenv("MAX_INPUT_TOKENS", "60000"))
