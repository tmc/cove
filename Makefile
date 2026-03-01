.PHONY: build sign agent test clean

build:
	go build -o vz-macos .

agent:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o cmd/vz-agent/vz-agent ./cmd/vz-agent

sign:
	codesign -s - -f --entitlements internal/autosign/vz.entitlements ./vz-macos

test:
	go test -v ./...

clean:
	rm -f vz-macos cmd/vz-agent/vz-agent
