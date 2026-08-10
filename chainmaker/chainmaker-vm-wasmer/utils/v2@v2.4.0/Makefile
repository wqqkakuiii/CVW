VERSION=v2.4.0_qc

gomod:
	go get chainmaker.org/chainmaker/common/v2@$(VERSION)
	go get chainmaker.org/chainmaker/pb-go/v2@$(VERSION)
	go get chainmaker.org/chainmaker/protocol/v2@$(VERSION)
	go mod tidy

lint:
	golangci-lint run ./...