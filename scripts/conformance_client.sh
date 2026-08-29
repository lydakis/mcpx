#!/bin/sh
set -eu

server_url=${1:?"usage: conformance_client.sh <server-url>"}
scenario=${MCP_CONFORMANCE_SCENARIO:-}
mcpx_bin=${MCPX_CONFORMANCE_BIN:-./mcpx}

case "$scenario" in
  initialize)
    exec "$mcpx_bin" "$server_url" --json
    ;;
  tools_call|tools-call)
    catalog=$("$mcpx_bin" "$server_url" --json)
    tool=$(printf '%s' "$catalog" | python3 -c 'import json,sys; tools=json.load(sys.stdin); print(tools[0]["name"] if tools else "")')
    if [ -z "$tool" ]; then
      echo "conformance server advertised no tools" >&2
      exit 1
    fi
    exec "$mcpx_bin" "$server_url" "$tool" '{"a":2,"b":3}'
    ;;
  json-schema-ref-no-deref)
    exec "$mcpx_bin" "$server_url" --catalog
    ;;
  *)
    echo "unsupported mcpx conformance scenario: $scenario" >&2
    exit 2
    ;;
esac
