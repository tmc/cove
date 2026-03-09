.PHONY: build sign agent test clean proto proto-go proto-swift

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

# Proto generation targets.
# Requires: protoc (v6+), protoc-gen-go, protoc-gen-connect-go
# Optional: protoc-gen-swift (for Swift package)

proto: proto-go proto-swift

proto-go:
	cd proto && protoc --go_out=. --go_opt=paths=source_relative control.proto
	mv proto/control.pb.go proto/controlpb/control.pb.go
	cd proto && protoc --go_out=. --go_opt=paths=source_relative \
		--connect-go_out=. --connect-go_opt=paths=source_relative agent.proto
	mv proto/agent.pb.go proto/agentpb/agent.pb.go
	mv proto/agent.connect.go proto/agentpbconnect/agent.connect.go

proto-swift:
	@if command -v protoc-gen-swift >/dev/null 2>&1; then \
		mkdir -p swift/VZControl/Sources/VZControl/Generated; \
		cd proto && protoc --swift_out=../swift/VZControl/Sources/VZControl/Generated \
			--swift_opt=Visibility=Public control.proto; \
		echo "Swift proto generated."; \
	else \
		echo "protoc-gen-swift not found. Install: brew install swift-protobuf"; \
	fi
