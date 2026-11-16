#!/usr/bin/env bash
# 从契约中心仓同步最新契约。CI 会校验副本与契约仓一致性。
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
CONTRACT_REPO="${CONTRACT_REPO:-$HERE/../weixun-api-contract}"

if [ ! -d "$CONTRACT_REPO" ]; then
  echo "找不到契约仓: $CONTRACT_REPO" >&2
  exit 1
fi

cp "$CONTRACT_REPO/contract/weixun-api.json" "$HERE/contract/weixun-api.json"
sha="$(cd "$CONTRACT_REPO" && git rev-parse HEAD 2>/dev/null || echo uncommitted)"
echo "$sha" > "$HERE/contract/CONTRACT_VERSION"
echo "已同步契约，版本 $sha"
