.PHONY: test lint-spec build layer

test:
	go test ./...

lint-spec:
	npx --yes @redocly/cli@latest lint openapi.yaml

build:
	sam build -t template.yaml

layer:
	@echo "layer: filled in by T15"
