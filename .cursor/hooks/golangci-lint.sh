#!/bin/bash

input=$(cat)
file_path=$(echo "$input" | jq -r '.file_path // empty')

if [[ "$file_path" != *.go ]]; then
  exit 0
fi

cd "$CURSOR_PROJECT_DIR" || exit 0
output=$(golangci-lint run ./... 2>&1)
exit_code=$?

if [ $exit_code -ne 0 ]; then
  cat <<EOF
{
  "additional_context": "golangci-lint found issues after editing $file_path:\n$output\n\nFix these before proceeding."
}
EOF
else
  exit 0
fi
