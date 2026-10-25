.PHONY: build web dist release test vet fmt run

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/memory-co/agentuse/internal/cli.Version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
SERVER_PLATFORMS := $(PLATFORMS)

# 网页:frontend/ 构建到 frontend/dist,编译 agentuse-server 时嵌进去
web:
	cd frontend && npm ci && npm run build

# bin/agentuse(用户电脑)和 bin/agentuse-server(服务端，带网页)
build: web
	go build -ldflags "$(LDFLAGS)" -o bin/agentuse ./cmd/agentuse
	go build -ldflags "$(LDFLAGS)" -o bin/agentuse-server ./cmd/agentuse-server

# 交叉编译各平台的 agentuse 到 dist/,服务的 setup.sh 从这里下载
dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build dist/agentuse-$$os-$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/agentuse-$$os-$$arch ./cmd/agentuse || exit 1; \
	done

# 发布制品:dist/ 下 agentuse、agentuse-server 各平台 + SHA256SUMS
release: dist web
	@for p in $(SERVER_PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build dist/agentuse-server-$$os-$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/agentuse-server-$$os-$$arch ./cmd/agentuse-server || exit 1; \
	done
	cd dist && sha256sum agentuse-* > SHA256SUMS

run: build dist
	bin/agentuse-server start -dist dist

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
