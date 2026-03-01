module github.com/tmc/vz-macos

go 1.25.5

require (
	github.com/ebitengine/purego v0.10.0-alpha.5
	github.com/tmc/apple v0.0.0
	github.com/tmc/macgo v0.0.0-20260221201249-9f1975a72d07
	golang.org/x/crypto v0.47.0
	golang.org/x/image v0.33.0
	golang.org/x/sys v0.41.0
	golang.org/x/term v0.40.0
	golang.org/x/tools v0.40.0
	google.golang.org/grpc v1.78.0
	google.golang.org/protobuf v1.36.11
	rsc.io/script v0.0.2
)

require (
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/text v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251029180050-ab9386a59fda // indirect
)

replace github.com/tmc/apple => ../apple
