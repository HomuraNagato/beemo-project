#!/bin/sh

set -e

echo "Checking for model file: $ORPHEUS_MODEL_NAME  ---  https://huggingface.co/lex-au/${ORPHEUS_MODEL_NAME}/resolve/main/${ORPHEUS_MODEL_NAME}"

if [ ! -f "/app/models/${ORPHEUS_MODEL_NAME}" ]; then
  echo "Downloading model file..."
  wget -P /app/models "https://huggingface.co/lex-au/${ORPHEUS_MODEL_NAME}/resolve/main/${ORPHEUS_MODEL_NAME}"
else
  echo "Model file already exists"
fi
