.PHONY: test test-dynamo lint-spec build layer

test:
	go test ./...

# Runs the DynamoDB store contract against DynamoDB Local; the container is
# always removed, even when the tests fail.
test-dynamo:
	@docker rm -f audiofile-dynamo-test >/dev/null 2>&1 || true
	@docker run -d --rm --name audiofile-dynamo-test -p 127.0.0.1:8000:8000 amazon/dynamodb-local -jar DynamoDBLocal.jar -inMemory -sharedDb >/dev/null
	@ready=0; for i in $$(seq 1 30); do \
		if curl -s -o /dev/null http://127.0.0.1:8000; then ready=1; break; fi; sleep 1; done; \
	if [ $$ready -ne 1 ]; then echo "DynamoDB Local not ready after 30s" >&2; docker rm -f audiofile-dynamo-test >/dev/null; exit 1; fi; \
	DYNAMODB_ENDPOINT=http://127.0.0.1:8000 go test -count=1 -run TestDynamo -v ./internal/store/; \
	status=$$?; \
	docker rm -f audiofile-dynamo-test >/dev/null; \
	exit $$status

lint-spec:
	npx --yes @redocly/cli@latest lint openapi.yaml

build:
	sam build -t template.yaml

layer:
	@echo "layer: filled in by T15"
