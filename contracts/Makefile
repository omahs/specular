.PHONY: geth bindings install

GOBIN = ./../clients/geth/specular/build/bin

install: bindings geth

bindings:
	cd ..; go generate ./clients/geth/specular/...

geth: bindings
	go build -o $(GOBIN)/geth ./../clients/geth/specular/cmd/geth
	@echo "Done building."
	@echo "Run \"$(GOBIN)/geth\" to launch geth."
