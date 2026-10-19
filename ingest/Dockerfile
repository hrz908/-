FROM python:3.11-slim

WORKDIR /app

# 安装系统依赖（pymupdf 等可能需要）
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# 创建默认监听目录
RUN mkdir -p /app/data/docs

ENV WATCH_DIR=/app/data/docs
ENV PYTHONUNBUFFERED=1

CMD ["python", "main.py", "watch"]
