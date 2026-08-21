# 渠道可用性 API

## 接口地址

`POST /api/channel/availability`

该接口用于查询指定渠道在所选时间段内的请求数、错误数和可用率，仅管理员可以访问。鉴权请求头参见 [API 鉴权文档](./api_auth.md)。

## 请求体

```json
{
  "channel_ids": [1, 2],
  "period": "24h"
}
```

字段说明：

- `channel_ids`：渠道 ID 数组，渠道 ID 必须大于 `0`，重复 ID 会自动去重。
- `period`：统计周期，支持以下值：
  - `24h`：最近 24 小时，按 30 分钟聚合。
  - `today`：服务器本地时间的今天零点至当前时间，按 30 分钟聚合。
  - `7d`：最近 7 天，按 3 小时聚合。

## 响应示例

```json
{
  "success": true,
  "message": "",
  "data": [
    {
      "channel_id": 1,
      "request_count": 120,
      "error_count": 2,
      "availability": 98.33,
      "trend": [
        {
          "bucket_time": 1787268600,
          "request_count": 20,
          "error_count": 1,
          "availability": 95
        }
      ]
    }
  ]
}
```

可用率计算公式为：`(请求数 - 错误数) / 请求数 × 100%`。没有请求的渠道或时间段中，`availability` 为 `null`，且不参与整体可用率计算。

## 调用示例

```bash
curl -X POST 'https://your-domain.com/api/channel/availability' \
  -H 'Authorization: your_access_token' \
  -H 'New-Api-User: 1' \
  -H 'Content-Type: application/json' \
  -d '{
    "channel_ids": [1, 2],
    "period": "7d"
  }'
```

