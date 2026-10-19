# 部署说明

## 本地开发

1. 启动基础设施:
   ```bash
   cd server && docker compose up -d
   ```

2. 启动 Python Agent 服务:
   ```bash
   cd agent
   pip install -r requirements.txt
   export OPENAI_API_KEY=your_key
   export OPENAI_API_BASE=https://your-provider/v1
   uvicorn main:app --host 0.0.0.0 --port 8000 --reload
   ```

3. 启动 Go Gateway:
   ```bash
   cd server
   go run main.go
   ```

4. (可选) 启动 Ingest Watcher:
   ```bash
   cd ingest
   pip install -r requirements.txt
   python main.py watch
   ```

## 生产部署

- Nginx 配置: `deploy/nginx.conf`
- TLS 证书配置需自行添加到 nginx.conf 的 server 块
- 环境变量通过 docker-compose 或 systemd 注入
