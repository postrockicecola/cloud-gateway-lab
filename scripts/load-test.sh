#!/usr/bin/env bash
# Drive the Gateway so HPA / metrics have something to show.
set -euo pipefail

URL="${GATEWAY_URL:-http://127.0.0.1:8080}"
KEY="${GATEWAY_API_KEY:-sk-lab}"
MODEL="${MODEL:-gpt-5}"
N="${REQUESTS:-40}"

echo "POST ${URL}/v1/chat/completions  x${N}  model=${MODEL}"
ok=0
fail=0
for i in $(seq 1 "$N"); do
  code="$(curl -s -o /tmp/gateway-load-body -w '%{http_code}' \
    -H "Authorization: Bearer ${KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"load ${i}\"}]}" \
    "${URL}/v1/chat/completions" || echo 000)"
  if [ "$code" = "200" ]; then
    ok=$((ok + 1))
  else
    fail=$((fail + 1))
  fi
  printf '  [%02d] %s\n' "$i" "$code"
done

echo
echo "ok=${ok} fail=${fail}"
echo "status:  curl -s ${URL}/model-status"
echo "metrics: curl -s ${URL}/metrics"
echo
curl -s "${URL}/model-status" || true
echo
curl -s "${URL}/metrics" | head -n 12 || true
