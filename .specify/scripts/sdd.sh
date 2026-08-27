#!/usr/bin/env bash
# SDD workflow driver for multi-agent-system-turbo.
# Governed by .specify/memory/constitution.md — Article I (chain) and Article III (bilingual).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TPL="$REPO_ROOT/.specify/templates"
SPECS="$REPO_ROOT/specs"

die() { printf 'sdd: %s\n' "$*" >&2; exit 1; }
info() { printf '\033[32m==>\033[0m %s\n' "$*"; }

usage() {
  cat <<'USAGE'
Usage: sdd.sh <command> [args]

Commands:
  new-feature <slug>       Scaffold specs/NNN-slug/ with the full bilingual SDD chain.
  next-id                  Print the next free feature number.
  list                     List features and the status of each artifact.
  verify                   Run the machine checks (delegates to `go run ./cmd/sddctl verify`).
  amend <feature> <artifact> <version>
                           Stamp an artifact's version and re-stamp its downstream
                           derived_from_version entries as needing review.
  help                     This text.

The chain, per Constitution Article I.1:
  spec -> plan -> design-hld -> design-lld -> tasks -> code -> artifacts
USAGE
}

next_id() {
  local max=0 n
  shopt -s nullglob
  for d in "$SPECS"/*/; do
    n="$(basename "$d")"; n="${n%%-*}"
    [[ "$n" =~ ^[0-9]+$ ]] || continue
    (( 10#$n > max )) && max=$((10#$n))
  done
  printf '%03d\n' $((max + 1))
}

new_feature() {
  local slug="${1:-}"
  [[ -n "$slug" ]] || die "new-feature requires a slug, e.g. 'sdd.sh new-feature log-correlation'"
  [[ "$slug" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || die "slug must be kebab-case: [a-z0-9-]"
  local id; id="$(next_id)"
  local dir="$SPECS/$id-$slug"
  [[ -e "$dir" ]] && die "$dir already exists"
  mkdir -p "$dir"

  local pretty; pretty="$(tr '-' ' ' <<<"$slug")"
  for base in spec plan design-hld design-lld tasks; do
    for lang in "" ".zh"; do
      sed -e "s/NNN-slug/$id-$slug/g" \
          -e "s/\[FEATURE NAME\]/${pretty}/g" \
          -e "s/\[特性名称\]/${pretty}/g" \
          -e "s/YYYY-MM-DD/$(date -u +%F)/g" \
          "$TPL/$base-template${lang}.md" > "$dir/$base${lang}.md"
    done
  done
  sed "s/NNN-slug/$id-$slug/" "$TPL/traceability-template.yaml" > "$dir/traceability.yaml"

  info "created $dir"
  info "next: write spec.md AND spec.zh.md (Article III.2 — same commit), then run 'sdd.sh verify'"
}

list_features() {
  shopt -s nullglob
  local any=0
  for d in "$SPECS"/*/; do
    any=1
    printf '\033[1m%s\033[0m\n' "$(basename "$d")"
    for f in spec plan design-hld design-lld tasks; do
      local mark_en="·" mark_zh="·"
      [[ -f "$d/$f.md" ]] && mark_en="✓"
      [[ -f "$d/$f.zh.md" ]] && mark_zh="✓"
      printf '  %-14s en:%s zh:%s\n' "$f" "$mark_en" "$mark_zh"
    done
  done
  (( any )) || echo "(no features yet — run: sdd.sh new-feature <slug>)"
}

amend() {
  local feature="${1:-}" artifact="${2:-}" version="${3:-}"
  [[ -n "$feature" && -n "$artifact" && -n "$version" ]] || die "amend <feature> <artifact> <version>"
  local tf="$SPECS/$feature/traceability.yaml"
  [[ -f "$tf" ]] || die "no traceability.yaml for feature '$feature'"
  command -v go >/dev/null || die "amend needs the Go toolchain"
  ( cd "$REPO_ROOT" && go run ./cmd/sddctl amend --feature "$feature" --artifact "$artifact" --version "$version" )
}

case "${1:-help}" in
  new-feature) shift; new_feature "$@" ;;
  next-id)     next_id ;;
  list)        list_features ;;
  verify)      ( cd "$REPO_ROOT" && go run ./cmd/sddctl verify ) ;;
  amend)       shift; amend "$@" ;;
  help|-h|--help) usage ;;
  *) usage; die "unknown command: $1" ;;
esac
