# 通知 Webhook API

## 接口地址

`POST /api/notification/send`

## 鉴权方式

仅支持 `Bearer` 密钥鉴权。

请求头：

```http
Authorization: Bearer <NOTIFICATION_WEBHOOK_SECRET>
```

其中 `<NOTIFICATION_WEBHOOK_SECRET>` 来自环境变量 `NOTIFICATION_WEBHOOK_SECRET`。

## Query 参数

- `channel`：通知渠道，必填。支持：
  - `email`（或 `mail`）
  - `feishu`（或 `lark`）
  - `dingtalk`
- `format`：可选，默认空字符串
  - 空：按纯文本模式解析
  - `new-api`：按结构化格式解析

## 请求体

### 1) format 为空（纯文本）

支持以下任一形式：

1. 纯文本字符串（request body 直接放文本）
2. JSON 字符串，如 `"hello"`
3. JSON 对象（推荐）：

```json
{
  "title": "通知标题",
  "content": "通知内容"
}
```

对象中可选字段：`title/subject`、`content/message/text/body`。

### 2) format=new-api

```json
{
  "type": "quota_exceed",
  "title": "额度预警通知",
  "content": "您的额度即将用尽，当前剩余额度为 {{value}}",
  "values": [
    "$0.99"
  ],
  "timestamp": 1739950503
}
```

字段说明：
- `type`：通知类型（如 `quota_exceed`）
- `title`：通知标题
- `content`：通知内容，支持 `{{value}}` 占位符
- `values`：按顺序替换 `content` 中的 `{{value}}`
- `timestamp`：Unix 时间戳（秒）

## 响应示例

成功：

```json
{
  "success": true,
  "message": "通知发送成功",
  "data": {
    "channel": "email",
    "format": "new-api"
  }
}
```

失败：

```json
{
  "success": false,
  "message": "系统未配置 FEISHU_WEBHOOK_URL"
}
```

钉钉通知使用 `DINGTALK_WEBHOOK_URL`，加签机器人还需配置 `DINGTALK_WEBHOOK_SECRET`。这两项也可以在管理端的通知设置中维护。

钉钉群机器人的创建、Webhook 获取和安全设置请参考[钉钉自定义机器人接入文档](https://open.dingtalk.com/document/orgapp/custom-robot-access)。

## 调用示例

```bash
curl -X POST 'https://your-domain.com/api/notification/send?channel=feishu&format=new-api' \
  -H 'Authorization: Bearer your_webhook_secret' \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "quota_exceed",
    "title": "额度预警通知",
    "content": "您的额度即将用尽，当前剩余额度为 {{value}}",
    "values": ["$0.99"],
    "timestamp": 1739950503
  }'
```

钉钉渠道只需将 `channel` 改为 `dingtalk`：

```bash
curl -X POST 'https://your-domain.com/api/notification/send?channel=dingtalk&format=new-api' \
  -H 'Authorization: Bearer your_webhook_secret' \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "quota_exceed",
    "title": "额度预警通知",
    "content": "您的额度即将用尽，当前剩余额度为 {{value}}",
    "values": ["$0.99"],
    "timestamp": 1739950503
  }'
```
