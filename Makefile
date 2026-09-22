BINARY="convo"

all: goTool build

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./bin/${BINARY}

run:
	@go run ./main.go conf/config.yaml

rebuild-redis:
	@go run ./cmd/rebuild-redis --confirm-maintenance

verify:
	go test ./...
	go vet ./...
	go vet -tags=integration ./...

goTool:
	go fmt ./
	go vet ./

clean:
	@if [  -f ${BINARY} ]; then
	    rm ${BINARY};
	fi

help:
	@echo "make - 格式化 Go 代码，并编译生成二进制文件"
	@echo "make build - 编译 Go 代码，生成二进制文件"
	@echo "make run - 直接运行 Go 代码"
	@echo "make rebuild-redis - 在停写并排空消息后从 MySQL 重建 Redis 派生状态"
	@echo "make verify - 运行默认测试和静态检查（含 integration tag 编译）"
	@echo "make clean - 移除二进制文件和 vim swap files"
	@echo "make goTool - 运行 Go 工具 'fmt' and 'vet'"
