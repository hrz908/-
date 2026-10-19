"""LLM 工厂：统一走 OpenAI 兼容接口，通过 config 里的 base_url / api_key 配置。

大模型 (get_llm) 用于最终回答生成，小模型 (get_small_llm) 用于改写、分类等轻量任务。
"""
from langchain_openai import ChatOpenAI

from config import OPENAI_API_BASE, OPENAI_API_KEY, LLM_MODEL, SMALL_MODEL


def get_llm(streaming: bool = True, temperature: float = 0.7) -> ChatOpenAI:
    """主力对话模型：默认开启流式，温度略高以保证回答自然。"""
    return ChatOpenAI(
        model=LLM_MODEL,
        openai_api_base=OPENAI_API_BASE,
        openai_api_key=OPENAI_API_KEY,
        streaming=streaming,
        temperature=temperature,
    )


def get_small_llm(streaming: bool = False, temperature: float = 0.0) -> ChatOpenAI:
    """小模型：用于指代消解、意图分类、滚动摘要，温度 0 追求确定性。"""
    return ChatOpenAI(
        model=SMALL_MODEL,
        openai_api_base=OPENAI_API_BASE,
        openai_api_key=OPENAI_API_KEY,
        streaming=streaming,
        temperature=temperature,
    )
