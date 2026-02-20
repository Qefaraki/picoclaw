#!/bin/bash
# Email Digest — reads email_digest.jsonl and outputs summary, then truncates
# Used by the morning briefing to include email summaries
#
# Output: JSON array of digest entries, then clears the file

WORKSPACE="${PICOCLAW_WORKSPACE:-/data/picoclaw/workspace}"
DIGEST_FILE="${WORKSPACE}/email_digest.jsonl"

if [ ! -f "$DIGEST_FILE" ] || [ ! -s "$DIGEST_FILE" ]; then
    echo "[]"
    exit 0
fi

# Read all entries into a JSON array
echo "["
first=true
while IFS= read -r line; do
    if [ -n "$line" ]; then
        if [ "$first" = true ]; then
            first=false
        else
            echo ","
        fi
        echo "$line"
    fi
done < "$DIGEST_FILE"
echo "]"

# Clear the digest file after reading
> "$DIGEST_FILE"
