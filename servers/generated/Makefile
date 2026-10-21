# sam build runs build-GeneratedServerFunction in a scratch copy of app/
# (the CodeUri); template.yaml points ContextPath here. app/ must be patched
# first: npm ci needs the package-lock.json that the patch script creates.
HERE := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))

.PHONY: build-GeneratedServerFunction
build-GeneratedServerFunction:
	@test -f src/machine-auth.ts || { echo "app/ is not patched: run scripts/patch-generated-server.sh servers/generated/app" >&2; exit 1; }
	npm ci --no-audit --no-fund --loglevel=error
	npm run build
	npm prune --omit=dev --no-audit --no-fund --loglevel=error
	cp -R build node_modules package.json $(ARTIFACTS_DIR)/
	cp $(HERE)run.sh $(ARTIFACTS_DIR)/run.sh
	chmod 755 $(ARTIFACTS_DIR)/run.sh
