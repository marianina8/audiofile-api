# Builds the FFmpegLayer: static arm64 ffmpeg and ffprobe in /opt/bin, copied
# from a digest-pinned mwader/static-ffmpeg image and checksum-verified.
# sam build calls build-FFmpegLayer with ARTIFACTS_DIR set; `make layer` at the
# repo root calls it with ARTIFACTS_DIR=layers/ffmpeg (bin/ is git-ignored).

FFMPEG_IMAGE   := mwader/static-ffmpeg:9.0.2-1-arm64@sha256:a4207247c686fbe1753039214d04df2dc5be2ecd9aa233a122912aa31984d67c
FFMPEG_SHA256  := fd63f670fb1ad8a9ce069bbdf01ffcba5de9b3a33531c220df139b3c1a8f7b0a
FFPROBE_SHA256 := ecd5b709f4e311a7ef9a759f803d4b2dc21fe57e4cca3d1d4a034ecbe4c4cb13

# sha256sum on Linux, shasum -a 256 on macOS.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

.PHONY: build-FFmpegLayer

build-FFmpegLayer:
	@test -n "$(ARTIFACTS_DIR)" || { echo "ARTIFACTS_DIR is not set" >&2; exit 1; }
	@command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 \
		|| { echo "docker is required to build FFmpegLayer (install Docker and start it)" >&2; exit 1; }
	docker pull --quiet --platform linux/arm64 "$(FFMPEG_IMAGE)"
	rm -rf "$(ARTIFACTS_DIR)/bin"
	mkdir -p "$(ARTIFACTS_DIR)/bin"
	@set -e; \
	cid=$$(docker create --platform linux/arm64 "$(FFMPEG_IMAGE)"); \
	trap 'docker rm "$$cid" >/dev/null' EXIT; \
	docker cp "$$cid:/ffmpeg" "$(ARTIFACTS_DIR)/bin/ffmpeg"; \
	docker cp "$$cid:/ffprobe" "$(ARTIFACTS_DIR)/bin/ffprobe"
	@set -e; for pair in "ffmpeg $(FFMPEG_SHA256)" "ffprobe $(FFPROBE_SHA256)"; do \
		set -- $$pair; \
		got=$$($(SHA256) "$(ARTIFACTS_DIR)/bin/$$1" | cut -d' ' -f1); \
		if [ "$$got" != "$$2" ]; then \
			echo "$$1 checksum mismatch (got $$got, want $$2); refusing to build the layer" >&2; \
			rm -rf "$(ARTIFACTS_DIR)/bin"; exit 1; \
		fi; \
		echo "$$1: sha256 OK"; \
	done
	chmod 0755 "$(ARTIFACTS_DIR)/bin/ffmpeg" "$(ARTIFACTS_DIR)/bin/ffprobe"
