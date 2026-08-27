#!/usr/bin/env bash
# Assert the release artifacts are usable, not merely produced.
#
# This exists because `make dist` used to be "did the commands exit 0", and the
# first attempt to cut v0.1.0 found out on the tag push that they did not. Even
# once they did, the tarball still shipped a manual with a dead link — an
# artifact that builds is not an artifact that works.
#
# Everything here is checked against the *extracted tarball*, never against the
# repository, because the repository is not what a user downloads.
set -euo pipefail

DIST="${1:-dist}"
VERSION="${2:-dev}"

fail() { echo "verify-dist: $*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "go is required to name the host platform"
HOST="$(go env GOOS)-$(go env GOARCH)"
TARBALL="$DIST/mas-$HOST.tar.gz"
[ -f "$TARBALL" ] || fail "no tarball for this host ($TARBALL); nothing can be checked by running it"

# 1. Checksums describe exactly the published set — every tarball, once, and
#    nothing that is not published. A downloader runs `sha256sum -c SHA256SUMS`
#    against what they fetched; an entry naming a file the release does not
#    carry makes that command fail on a perfectly good download.
[ -f "$DIST/SHA256SUMS" ] || fail "no SHA256SUMS"
dupes=$(awk '{print $2}' "$DIST/SHA256SUMS" | sort | uniq -d)
[ -z "$dupes" ] || fail "SHA256SUMS lists these more than once: $dupes"
listed=$(awk '{print $2}' "$DIST/SHA256SUMS" | sort)
present=$(cd "$DIST" && ls mas-*.tar.gz | sort)
[ "$listed" = "$present" ] || fail "SHA256SUMS does not match the published tarballs:
  listed:  $(echo "$listed" | tr '\n' ' ')
  present: $(echo "$present" | tr '\n' ' ')"
( cd "$DIST" && sha256sum -c SHA256SUMS >/dev/null ) || fail "a checksum does not match its file"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
tar -xzf "$TARBALL" -C "$work"

# 2. One top-level directory: extracting must not scatter files into the cwd.
top=$(tar -tzf "$TARBALL" | cut -d/ -f1 | sort -u)
[ "$(printf '%s\n' "$top" | wc -l)" -eq 1 ] || fail "the tarball extracts more than one top-level entry: $top"
root="$work/$top"

# 3. The binary in the package runs, and is the version we claim to be shipping.
[ -x "$root/mas" ] || fail "the packaged mas is missing or not executable"
reported="$("$root/mas" version)"
case "$reported" in
  *"$VERSION"*) ;;
  *) fail "the packaged binary reports '$reported', which does not carry $VERSION" ;;
esac

# 4. The example configuration it ships is one the binary accepts. A sample that
#    does not load is worse than none: it is the first thing a new user copies.
#
#    `config` rather than `doctor` on purpose: doctor's exit code grades the
#    configuration's health, and the sample points at telemetry endpoints that
#    are unreachable from a build runner — a warning that is correct and has
#    nothing to say about whether the file parses.
#
#    The two absolute paths are redirected because the sample writes them for
#    the container, where the image creates /var/lib/mas and gives it to uid
#    65532. A packaging host is not that container and should not have to look
#    like one; everything else about the file — syntax, unknown fields,
#    validation — is still under test. (This is how CI found the check: it
#    passed here as root and failed on a runner that cannot mkdir /var/lib.)
[ -f "$root/mas.example.yaml" ] || fail "no example configuration in the package"
MAS_STORE_DIR="$work/store" MAS_SOURCE_CACHE_DIR="$work/src" \
  "$root/mas" config --config "$root/mas.example.yaml" >/dev/null 2>&1 \
  || fail "the shipped mas.example.yaml is not loadable by the shipped binary"

# 5. Both languages, everywhere. Article III is not suspended at packaging time.
for f in README.md README.zh.md LICENSE; do
  [ -f "$root/$f" ] || fail "the package is missing $f"
done
for lang in en zh; do
  [ -d "$root/docs/$lang" ] || fail "the package has no docs/$lang"
done
en_count=$(find "$root/docs/en" -name '*.md' | wc -l)
zh_count=$(find "$root/docs/zh" -name '*.md' | wc -l)
[ "$en_count" -eq "$zh_count" ] || fail "docs/en has $en_count files and docs/zh has $zh_count"
[ "$en_count" -gt 0 ] || fail "no documentation was packaged at all"

# 6. No dead links between the shipped docs. The manual linked to evaluation.md
#    while packaging shipped a hand-picked subset that omitted it, so the
#    downloaded manual pointed at nothing.
dead=0
while IFS= read -r doc; do
  dir=$(dirname "$doc")
  while IFS= read -r target; do
    [ -z "$target" ] && continue
    if [ ! -e "$dir/$target" ]; then
      echo "verify-dist: ${doc#$root/} links to $target, which is not in the package" >&2
      dead=1
    fi
  done < <(grep -o '](\./[A-Za-z0-9._/-]*\.md' "$doc" | sed 's/](\.\///' | sort -u)
done < <(find "$root/docs" -name '*.md')
[ "$dead" -eq 0 ] || fail "the package ships documentation with dead links"

echo "verify-dist: $top is complete, bilingual, self-consistent and runnable"
