.PHONY: test test-dynamo lint-spec build layer validate validate-auth build-auth validate-mcphost build-mcphost build-ApiFunction build-ProcessorFunction build-BridgeFunction build-AuthorizerFunction build-McpTestFunction validate-mcptest build-mcptest validate-generated build-generated validate-curated build-curated

LAMBDA_GO := GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -ldflags="-s -w"

test:
	go test ./...

# Runs the DynamoDB store and OAuth state contracts against DynamoDB Local;
# the container is always removed, even when the tests fail.
test-dynamo:
	@docker rm -f audiofile-dynamo-test >/dev/null 2>&1 || true
	@docker run -d --rm --name audiofile-dynamo-test -p 127.0.0.1:8000:8000 amazon/dynamodb-local -jar DynamoDBLocal.jar -inMemory -sharedDb >/dev/null
	@ready=0; for i in $$(seq 1 30); do \
		if curl -s -o /dev/null http://127.0.0.1:8000; then ready=1; break; fi; sleep 1; done; \
	if [ $$ready -ne 1 ]; then echo "DynamoDB Local not ready after 30s" >&2; docker rm -f audiofile-dynamo-test >/dev/null; exit 1; fi; \
	DYNAMODB_ENDPOINT=http://127.0.0.1:8000 go test -count=1 -run TestDynamo -v ./internal/store/ ./internal/oauth/; \
	status=$$?; \
	docker rm -f audiofile-dynamo-test >/dev/null; \
	exit $$status

lint-spec:
	npx --yes @redocly/cli@2.69.0 lint openapi.yaml

# sam build runs the build-<LogicalId> targets below (BuildMethod: makefile)
# and layers/ffmpeg/Makefile for FFmpegLayer.
build: validate
	sam build -t template.yaml

validate:
	sam validate --lint -t template.yaml

# The OAuth bridge stack (mcp-auth); its BridgeFunction builds from the repo root.
validate-auth:
	sam validate --lint -t auth/template.yaml

build-auth: validate-auth
	cd auth && sam build -t template.yaml

# The reusable mcp-host nested app; its AuthorizerFunction builds from the repo root.
validate-mcphost:
	sam validate --lint -t mcphost/template.yaml

build-mcphost: validate-mcphost
	cd mcphost && sam build -t template.yaml

# Copies ffmpeg/ffprobe from the digest-pinned static-ffmpeg image (needs Docker),
# verifies their checksums and puts them in layers/ffmpeg/bin/ (git-ignored).
layer:
	$(MAKE) -C layers/ffmpeg build-FFmpegLayer ARTIFACTS_DIR=$(CURDIR)/layers/ffmpeg

build-ApiFunction:
	$(LAMBDA_GO) -o $(ARTIFACTS_DIR)/bootstrap ./cmd/api

build-ProcessorFunction:
	$(LAMBDA_GO) -o $(ARTIFACTS_DIR)/bootstrap ./cmd/processor

build-BridgeFunction:
	$(LAMBDA_GO) -o $(ARTIFACTS_DIR)/bootstrap ./cmd/mcpauth

build-AuthorizerFunction:
	$(LAMBDA_GO) -o $(ARTIFACTS_DIR)/bootstrap ./cmd/mcpauthorizer

build-McpTestFunction:
	$(LAMBDA_GO) -o $(ARTIFACTS_DIR)/bootstrap ./servers/mcptest

# The throwaway test MCP server; nests mcphost/template.yaml, so building it
# also builds the mcp-host AuthorizerFunction.
validate-mcptest:
	sam validate --lint -t servers/mcptest/template.yaml

build-mcptest: validate-mcptest
	cd servers/mcptest && sam build -t template.yaml

# Server A, generated on camera into servers/generated/app (scripts/generate-server.sh --patch).
# Its function builds with servers/generated/Makefile; the nested mcp-host
# AuthorizerFunction builds from the repo root.
validate-generated:
	sam validate --lint -t servers/generated/template.yaml

build-generated: validate-generated
	cd servers/generated && sam build -t template.yaml

# Server B, the curated MCP server (internal/curated); nests mcphost/template.yaml.
build-CuratedServerFunction:
	$(LAMBDA_GO) -o $(ARTIFACTS_DIR)/bootstrap ./servers/curated

validate-curated:
	sam validate --lint -t servers/curated/template.yaml

build-curated: validate-curated
	cd servers/curated && sam build -t template.yaml
