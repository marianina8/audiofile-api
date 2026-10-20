# Builds the FFmpegLayer: static arm64 ffmpeg and ffprobe in /opt/bin.
# sam build calls build-FFmpegLayer with ARTIFACTS_DIR set; `make layer` at the
# repo root calls it with ARTIFACTS_DIR=layers/ffmpeg (bin/ is git-ignored).

FFMPEG_VERSION := 7.0.2
FFMPEG_DIR     := ffmpeg-$(FFMPEG_VERSION)-arm64-static
FFMPEG_TARBALL := $(FFMPEG_DIR).tar.xz
FFMPEG_URL     ?= https://johnvansickle.com/ffmpeg/old-releases/$(FFMPEG_TARBALL)
FFMPEG_SHA256  :=

# The download cache sits next to this Makefile (git-ignored).
CACHE_DIR ?= $(abspath $(dir $(lastword $(MAKEFILE_LIST))))/.cache
TARBALL   := $(CACHE_DIR)/$(FFMPEG_TARBALL)

# sha256sum on Linux, shasum -a 256 on macOS.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

.PHONY: build-FFmpegLayer verify

build-FFmpegLayer: verify
	@test -n "$(ARTIFACTS_DIR)" || { echo "ARTIFACTS_DIR is not set" >&2; exit 1; }
	rm -rf "$(ARTIFACTS_DIR)/bin"
	mkdir -p "$(ARTIFACTS_DIR)/bin"
	tar -xJf "$(TARBALL)" -C "$(ARTIFACTS_DIR)/bin" --strip-components=1 \
		"$(FFMPEG_DIR)/ffmpeg" "$(FFMPEG_DIR)/ffprobe"
	chmod 0755 "$(ARTIFACTS_DIR)/bin/ffmpeg" "$(ARTIFACTS_DIR)/bin/ffprobe"

# Checks the cached tarball against the pinned checksum on every build.
verify: $(TARBALL)
	@test -n "$(FFMPEG_SHA256)" || { echo "FFMPEG_SHA256 is not pinned" >&2; exit 1; }
	@echo "$(FFMPEG_SHA256)  $(TARBALL)" | $(SHA256) -c - >/dev/null \
		|| { echo "checksum mismatch for $(TARBALL); delete it and retry" >&2; exit 1; }

$(TARBALL):
	mkdir -p "$(CACHE_DIR)"
	curl -fsSL --connect-timeout 20 --retry 3 -o "$@.part" "$(FFMPEG_URL)"
	mv "$@.part" "$@"
