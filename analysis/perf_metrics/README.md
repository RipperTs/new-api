# 性能指标分析脚本

本目录用于分析 `logs` 消费日志表的性能指标，脚本只执行只读查询。

## 使用方式

```bash
export DB_HOST=10.10.93.188
export DB_PORT=3306
export DB_NAME=proxy_api
export DB_USER=proxy_api
export DB_PASSWORD='***'

python3 analysis/perf_metrics/analyze_perf_metrics.py --days 30
```

结果会输出到 `analysis/perf_metrics/reports/`，包含：

- `perf_metrics_*.json`：完整指标
- `perf_metrics_*.csv`：核心摘要表

## 指标口径

- 数据源：`logs`
- 消费日志：`type = 2`
- 时间字段：`created_at`，Unix 秒
- 总 Tokens：`prompt_tokens + completion_tokens`
- 上下文：`prompt_tokens`
- TPS：按秒聚合的 Tokens 峰值
- RPM / TPM：按分钟聚合的请求数和 Tokens

