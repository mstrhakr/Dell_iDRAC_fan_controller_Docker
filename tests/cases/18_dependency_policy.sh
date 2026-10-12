#!/bin/bash

# SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
# SPDX-License-Identifier: AGPL-3.0-only

# Immutable refs prevent a moved upstream tag from changing executable CI code.
# Local actions are reviewed with the repository; container actions need digests.
function dependency_policy_ref_is_pinned() {
  local REF="$1"
  [[ "$REF" == ./* ]] ||
    [[ "$REF" =~ ^docker://.+@sha256:[a-f0-9]{64}$ ]] ||
    [[ "$REF" =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_./-]+@[a-f0-9]{40}$ ]]
}

function test_dependency_policy_action_refs_are_pinned() {
  if [[ ! -d "$REPO_ROOT/.github/workflows" ]]; then
    skip_test "no workflows in this checkout"
    return 0
  fi

  local FILE REF
  local UNPINNED=""
  local COUNT=0
  while IFS= read -r FILE; do
    while IFS= read -r REF; do
      COUNT=$((COUNT + 1))
      if ! dependency_policy_ref_is_pinned "$REF"; then
        UNPINNED+=" ${FILE#"$REPO_ROOT/"}:$REF"
      fi
    done < <(sed -nE "s/^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*[\"']?([^\"'[:space:]#]+).*/\2/p" "$FILE")
  done < <(find "$REPO_ROOT/.github" -type f \( -name '*.yml' -o -name '*.yaml' \) | sort)

  assert_not_equals "0" "$COUNT" "the pin check must not silently inspect nothing"
  assert_empty "$UNPINNED" "external actions must use full commit SHAs, not tags or branches"
}

function test_dependency_policy_ref_validation_rejects_mutable_versions() {
  local REF
  for REF in actions/checkout@v7 actions/checkout@main actions/checkout@1234 docker://alpine:latest "docker://alpine@$(printf 'a%.0s' {1..40})"; do
    if dependency_policy_ref_is_pinned "$REF"; then
      fail "mutable ref accepted: $REF"
    else
      pass
    fi
  done
  for REF in ./local-action "actions/checkout@$(printf 'a%.0s' {1..40})" "docker://alpine@sha256:$(printf 'a%.0s' {1..64})"; do
    if dependency_policy_ref_is_pinned "$REF"; then
      pass
    else
      fail "immutable/local ref rejected: $REF"
    fi
  done
}
