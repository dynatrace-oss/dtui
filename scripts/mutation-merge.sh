#!/usr/bin/env bash
# Merges sharded gremlins JSON reports (see mutation-shard.sh) into one report
# with the same shape, recomputing the totals from the per-mutant statuses.
#
#   scripts/mutation-merge.sh <report.json>... > merged.json
set -euo pipefail

(( $# > 0 )) || { echo "usage: mutation-merge.sh <report.json>..." >&2; exit 2; }

jq -s '
  [.[].files[]] as $files
  | [$files[].mutations[].status] as $s
  | ($s | map(select(. == "KILLED")) | length) as $killed
  | ($s | map(select(. == "LIVED")) | length) as $lived
  | ($s | map(select(. == "NOT COVERED")) | length) as $uncovered
  | {
      go_module: .[0].go_module,
      test_efficacy: (if $killed + $lived > 0 then $killed / ($killed + $lived) * 100 else 0 end),
      mutations_coverage: (if $killed + $lived + $uncovered > 0 then ($killed + $lived) / ($killed + $lived + $uncovered) * 100 else 0 end),
      mutants_total: ($killed + $lived),
      mutants_killed: $killed,
      mutants_lived: $lived,
      mutants_not_viable: ($s | map(select(. == "NOT VIABLE")) | length),
      mutants_not_covered: $uncovered,
      elapsed_time: ([.[].elapsed_time] | max),
      files: ($files | sort_by(.file_name))
    }
' "$@"
