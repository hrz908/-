FROM python:3.11-slim

WORKDIR /app

# 依赖全部有 manylinux 预编译 wheel（psycopg[binary]/numpy/pandas/pymupdf4llm/tiktoken 等），
# 无需 build-essential 现场编译；去掉 apt 步骤既避开 deb.debian.org 国内不可达，又减小镜像。
# 若将来加入需源码编译的依赖，再补 apt（记得先把源换成国内镜像，如 mirrors.aliyun.com）。

# pip 源：国内构建默认走清华源，海外可传 --build-arg 覆盖
ARG PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple

COPY requirements.txt requirements-embed.txt ./
RUN pip install --no-cache-dir -i ${PIP_INDEX_URL} -r requirements.txt

# 本地向量化依赖（torch 近 1GB）：只有跑 ashare profile 才需要，默认不装
ARG INSTALL_EMBED=0
RUN if [ "$INSTALL_EMBED" = "1" ]; then \
        pip install --no-cache-dir -i ${PIP_INDEX_URL} -r requirements-embed.txt; \
    fi

COPY . .

# 创建默认监听目录与 A股语料目录
RUN mkdir -p /app/data/docs /app/data/ashare

ENV WATCH_DIR=/app/data/docs
ENV PYTHONUNBUFFERED=1

CMD ["python", "main.py", "watch"]
