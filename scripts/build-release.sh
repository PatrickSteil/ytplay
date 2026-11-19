#!/usr/bin/env bash
# Build ytplay for every supported platform into DIR, as
# ytplay_<version>_<os>_<arch>.tar.gz plus a SHA256SUMS file.
#
#   scripts/build-release.sh [DIR [VERSION]]      (defaults: dist, dev)
#
# ytplay has no cgo, so all of them cross-compile from any machine with Go.
# mpv drives over a Unix socket, so Windows is not a target.
set -euo pipefail

dir=${1:-dist}
version=${2:-dev}
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

cd "$(dirname "$0")/.."
rm -rf "$dir"
mkdir -p "$dir"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for target in $targets; do
	os=${target%/*}
	arch=${target#*/}
	name="ytplay_${version}_${os}_${arch}"
	echo "building $name"
	mkdir -p "$stage/$name"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch \
		go build -trimpath -ldflags "-s -w -X main.version=${version}" -o "$stage/$name/ytplay" ./cmd/ytplay
	cp README.md LICENSE "$stage/$name/"
	tar -C "$stage" -czf "$dir/$name.tar.gz" "$name"
done

(cd "$dir" && sha256sum ytplay_*.tar.gz >SHA256SUMS 2>/dev/null || shasum -a 256 ytplay_*.tar.gz >SHA256SUMS)
echo "done: $(ls "$dir" | wc -l) files in $dir"
