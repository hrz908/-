"""LLM 工厂：统一走 OpenAI 兼容接口，base_url / api_key 由 config 提供。

大模型（get_llm）用于最终回答生成，小模型（get_small_llm）用于改写、分类等轻量任务。
"""
from langchain_openai import ChatOpenAI

from config import LLM_TIMEOUT, LLM_MODEL, OPENAI_API_BASE, OPENAI_API_KEY, SMALL_MODEL


def get_llm(streaming: bool = True, temperature: float = 0.7) -> ChatOpenAI:
    """主力对话模型：默认流式，温度略高以保证回答自然。

    timeout 是单次请求硬上限：工具链有 8s 兜底，LLM 生成之前没有，
    不设会让整个事件流无限期挂住（Go 网关 runTimeout 到点砍流，用户只看到断流、没有解释）。
    """
    return ChatOpenAI(
        model=LLM_MODEL,
        openai_api_base=OPENAI_API_BASE,
        openai_api_key=OPENAI_API_KEY,
        streaming=streaming,
        temperature=temperature,
        timeout=LLM_TIMEOUT,
    )


def get_small_llm(streaming: bool = False, temperature: float = 0.0) -> ChatOpenAI:
    """小模型：用于指代消解、意图分类、滚动摘要，温度 0 追求确定性。"""
    return ChatOpenAI(
        model=SMALL_MODEL,
        openai_api_base=OPENAI_API_BASE,
        openai_api_key=OPENAI_API_KEY,
        streaming=streaming,
        temperature=temperature,
        timeout=LLM_TIMEOUT,
    )


def estimate_tokens(text: str) -> int:
    """粗略估算 token 数：优先 tiktoken，失败按字符数估算。"""
    if not text:
        return 0
    try:
        import tiktoken
        enc = tiktoken.get_encoding("cl100k_base")
        return len(enc.encode(text))
    except Exception:  # noqa: BLE001
        # 中英混合下 ~2 char/token 的保守估计
        return max(1, len(text) // 2)


def count_message_tokens(messages: list) -> int:
    """统计一组消息的 token 总量（发送前的上下文预算用）。"""
    text = ""
    for m in messages:
        c = getattr(m, "content", "")
        text += c if isinstance(c, str) else str(c)
    return estimate_tokens(text)


def is_context_overflow(exc: Exception) -> bool:
    """判断异常是否为「上下文超长」。

    上游 OpenAI 兼容接口输入超限时返回 400，例如：
      'number of input tokens (301046) has exceeded max_seq_len (163840) limit'
    这类错误冒泡出去整条回答就是空的（只剩一个 error 事件），必须识别后就地降级。
    """
    msg = str(exc).lower()
    return (
        "max_seq_len" in msg
        or "context length" in msg
        or "maximum context" in msg
        or "too many tokens" in msg
        or "reduce the length" in msg
    )
