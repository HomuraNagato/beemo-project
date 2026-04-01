#!/bin/bash

# Script to call /speak endpoint with text and voice parameters

HOST="100.73.65.14"
PORT="5022"
TEXT="${1:-Hello}"
VOICE="${2:-tara}"

URL="http://${HOST}:${PORT}/speak"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Text-to-Speech /speak API Request"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Endpoint: $URL"
echo "Text:     $TEXT"
echo "Voice:    $VOICE"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

REQUEST_BODY=$(jq -n --arg text "$TEXT" --arg voice "$VOICE" '
{
  text: $text,
  voice: $voice
}
')

RESPONSE=$(curl -s -X POST "$URL" \
  -H "Content-Type: application/json" \
  -d "$REQUEST_BODY" \
  -w "\n%{http_code}")

HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
JSON_RESPONSE=$(echo "$RESPONSE" | head -n -1)

echo "Response:"
echo "─────────────────────────────────────────────"
if echo "$JSON_RESPONSE" | jq . 2>/dev/null; then
  :
else
  echo "$JSON_RESPONSE"
fi
echo "─────────────────────────────────────────────"
echo "HTTP Status: $HTTP_CODE"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
