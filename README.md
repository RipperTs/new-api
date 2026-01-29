![new-api](/web/public/logo.png)

# New API


🍥新一代大模型网关与AI资产管理系统

<a href="https://trendshift.io/repositories/8227" target="_blank"><img src="https://trendshift.io/api/badge/repositories/8227" alt="Calcium-Ion%2Fnew-api | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>

<p align="center">
  <a href="https://raw.githubusercontent.com/Calcium-Ion/new-api/main/LICENSE">
    <img src="https://img.shields.io/github/license/Calcium-Ion/new-api?color=brightgreen" alt="license">
  </a>
  <a href="https://github.com/Calcium-Ion/new-api/releases/latest">
    <img src="https://img.shields.io/github/v/release/Calcium-Ion/new-api?color=brightgreen&include_prereleases" alt="release">
  </a>
  <a href="https://github.com/users/Calcium-Ion/packages/container/package/new-api">
    <img src="https://img.shields.io/badge/docker-ghcr.io-blue" alt="docker">
  </a>
  <a href="https://hub.docker.com/r/CalciumIon/new-api">
    <img src="https://img.shields.io/badge/docker-dockerHub-blue" alt="docker">
  </a>
  <a href="https://goreportcard.com/report/github.com/Calcium-Ion/new-api">
    <img src="https://goreportcard.com/badge/github.com/Calcium-Ion/new-api" alt="GoReportCard">
  </a>
</p>
</div>

## 📝 项目说明

> [!NOTE]  
> 本项目为开源项目，在[New API](https://github.com/QuantumNous/new-api)的基础上进行二次开发, 主要新增了邮件通知、自动检测等功能，感谢原作者的无私奉献。

> [!IMPORTANT]  
> - 使用者必须在遵循 OpenAI 的[使用条款](https://openai.com/policies/terms-of-use)以及**法律法规**的情况下使用，不得用于非法用途。
> - 本项目仅供个人学习使用，不保证稳定性，且不提供任何技术支持。
> - 根据[《生成式人工智能服务管理暂行办法》](http://www.cac.gov.cn/2023-07/13/c_1690898327029107.htm)的要求，请勿对中国地区公众提供一切未经备案的生成式人工智能服务。

## ✨ 主要特性

1. 渠道请求失败邮件通知
2. 每间隔30分钟自动检测仅自动禁用的渠道, 如正常则自动启用
3. 支持自定义请求头
4. 渠道支持设置代理
5. 支持Embedding模型测试
6. 支持 Claude Code 的渠道（镜像站）
7. 支持 Codex 的渠道（镜像站）


## 🚀 Docker 部署

镜像地址: `registry.cn-hangzhou.aliyuncs.com/ripper/new-api:latest`  

### Docker Compose 参考
```yml
version: '3.4'

services:
  new-api:
    image: registry.cn-hangzhou.aliyuncs.com/ripper/new-api:latest
    container_name: new-api
    restart: always
    command: --log-dir /app/logs
    ports:
      - "3800:3000"
    volumes:
      - ./:/data
      - ./logs:/app/logs
    environment:
      - SQL_DSN=mysql://user:password@tcp(127.0.0.1:3306)/dbname?parseTime=true
      - REDIS_CONN_STRING=redis://redis
      - SESSION_SECRET=b2cDFetMKbWqR3fc  # 修改为随机字符串
      - TZ=Asia/Shanghai
      - STREAMING_TIMEOUT=300
      - GENERATE_DEFAULT_TOKEN=true
      - UPDATE_TASK=false
      - NOTIFICATION_EMAIL=111@qq.com
      - BATCH_UPDATE_ENABLED=false
      - NODE_TYPE=master
#      - NODE_TYPE=slave  # 多机部署时从节点取消注释该行
#      - SYNC_FREQUENCY=60  # 需要定期从数据库加载数据时取消注释该行
#      - FRONTEND_BASE_URL=https://openai.justsong.cn  # 多机部署时从节点取消注释该行

    depends_on:
      - redis
    healthcheck:
      test: [ "CMD-SHELL", "wget -q -O - http://localhost:3000/api/status | grep -o '\"success\":\\s*true' | awk -F: '{print $2}'" ]
      interval: 30s
      timeout: 10s
      retries: 3

  redis:
    image: registry.cn-hangzhou.aliyuncs.com/ripper/redis:latest
    container_name: redis
    restart: always
```

## API特别说明
### 兼容 Gemini 的视频理解
“OpenAI 风格”的 /v1/chat/completions 这样传（务必走 Gemini 渠道/模型）：
```json
  {
    "model": "gemini-3-flash-preview",
    "messages": [{
      "role": "user",
      "content": [
        { "type": "text", "text": "Please summarize the video in 3 sentences." },
        { "type": "file_data", "file_data": { "file_uri": "https://www.youtube.com/watch?v=9hE5-98ZeCg" } }
      ]
    }]
  }
```
