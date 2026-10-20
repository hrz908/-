FROM python:3.11-slim

WORKDIR /app

# 依赖全部有 manylinux 预编译 wheel（psycopg[binary]/numpy/pandas/pymupdf4llm/tiktoken 等），
# 无需 build-essential 现场编译；去掉 apt 步骤既避开 deb.debian.org 国内不可达，又减小镜像。
# 若将来加入需源码编译的依赖，再补 apt（记得先把源换成国内镜像，如 mirrors.aliyun.com）。

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# 创建默认监听目录
RUN mkdir -p /app/data/docs

ENV WATCH_DIR=/app/data/docs
ENV PYTHONUNBUFFERED=1

CMD ["python", "main.py", "watch"]
