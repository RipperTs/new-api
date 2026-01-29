#!/usr/bin/env bash
set -euo pipefail

# 版本号统一逻辑：
# - 如果是 tag 触发（GitHub Actions），直接用 tag 名作为版本号（建议 vX.Y.Z）。
# - 其它情况：v0.0.0-<branch>.<shortsha>

ref_type="${GITHUB_REF_TYPE:-}"
ref_name="${GITHUB_REF_NAME:-}"

if [[ "$ref_type" == "tag" && -n "$ref_name" ]]; then
  version="$ref_name"
else
  # 本地/分支构建：尽量使用分支名 + commit 短 SHA，保证可追溯且格式稳定
  branch="${ref_name:-$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo dev)}"
  sha="$(git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)"

  # SemVer prerelease 允许: [0-9A-Za-z.-]
  safe_branch="$(echo "$branch" | tr '/_' '--' | tr -cd '0-9A-Za-z.-')"
  safe_branch="$(echo "$safe_branch" | sed 's/^\.*//; s/\.*$//')"
  if [[ -z "$safe_branch" ]]; then
    safe_branch="dev"
  fi

  version="v0.0.0-${safe_branch}.${sha}"
fi

# 强制带 v 前缀（common.Version、前端展示保持一致）
if [[ "$version" != v* ]]; then
  version="v${version}"
fi

echo "$version"

