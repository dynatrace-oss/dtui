#!/usr/bin/env bash
# Prints the gremlins --exclude-files flags that restrict a run to one shard
# of the module, so the full mutation run can split across parallel jobs.
#
#   scripts/mutation-shard.sh <dry-run.json> <index> <total>    # index is 1-based
#
# <dry-run.json> is `gremlins unleash --dry-run -o` output. Files go to the
# lightest shard, most runnable mutants first, so shards finish at roughly
# the same time. The split depends only on the tree, so every job computes
# the same one.
set -euo pipefail

usage="usage: mutation-shard.sh <dry-run.json> <index> <total>"
dry="${1:?$usage}"
index="${2:?$usage}"
total="${3:?$usage}"

# --exclude-files replaces the .gremlins.yaml list instead of adding to it,
# so repeat that list first.
sed -n 's/^ *- "\(.*\)"$/-E \1 /p' .gremlins.yaml | tr -d '\n'

jq -r '.files[] | "\([.mutations[] | select(.status == "RUNNABLE")] | length) \(.file_name)"' "$dry" |
  sort -k1,1nr -k2 |
  awk -v n="$total" -v want="$index" '
    {
      best = 1
      for (s = 2; s <= n; s++) if (load[s] < load[best]) best = s
      load[best] += $1
      if (best != want) {
        f = $2
        gsub(/[.]/, "[.]", f)
        printf "-E ^%s$ ", f
      }
    }'
