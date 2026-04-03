module github.com/tmc/vz-macos

go 1.25.5

require (
	connectrpc.com/connect v1.19.1
	github.com/ebitengine/purego v0.11.0-alpha.1.0.20260318130922-386f7c8fb549
	github.com/gorilla/websocket v1.5.3
	github.com/tmc/apple v0.5.1
	github.com/tmc/macgo v0.1.0
	golang.org/x/crypto v0.49.0
	golang.org/x/net v0.52.0
	golang.org/x/sys v0.42.0
	golang.org/x/term v0.41.0
	golang.org/x/tools v0.43.0
	google.golang.org/protobuf v1.36.11
	rsc.io/script v0.0.2
)

require (
	golang.org/x/image v0.38.0 // indirect
	golang.org/x/text v0.35.0 // indirect
)

replace github.com/tmc/apple => /Users/tmc/go/src/github.com/tmc/apple

replace github.com/tmc/macgo => /Volumes/tmc/go/src/github.com/tmc/macgo
