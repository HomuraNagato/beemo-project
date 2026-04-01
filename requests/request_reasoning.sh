#!/bin/bash

# Test script for llama.cpp server OpenAI-compatible chat completions API

HOST="0.0.0.0"
PORT=5014
PROMPT="${3:-explain what an LLM is in two paragraphs}"

#!/bin/sh

REASONING_HOST=0.0.0.0
REASONING_PORT=5014

# Test prompt
PROMPT="describe what an LLM is"

echo "Sending test request to llama.cpp reasoning server on port $REASONING_PORT..."

curl -X POST "http://${REASONING_HOST}:${REASONING_PORT}/v1/completions" \
     -H "Content-Type: application/json" \
     -d "{
           \"prompt\": \"${PROMPT}\",
           \"max_tokens\": 50,
           \"temperature\": 0.7
         }"

echo "\nRequest complete."
