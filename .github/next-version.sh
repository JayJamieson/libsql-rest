#!/usr/bin/env bash
set -euo pipefail

if ! latest=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null); then
  echo v1.0.0
  exit 0
fi

IFS=. read -r major minor patch <<<"${latest#v}"
subject=$(git log -1 --format=%s)
body=$(git log -1 --format=%b)
breaking='^[a-z]+(\([^)]*\))?!:'
feature='^feat(\([^)]*\))?:'

if [[ $subject =~ $breaking ]] || grep -q '^BREAKING CHANGE:' <<<"$body"; then
  echo "v$((major + 1)).0.0"
elif [[ $subject =~ $feature ]]; then
  echo "v${major}.$((minor + 1)).0"
else
  echo "v${major}.${minor}.$((patch + 1))"
fi
