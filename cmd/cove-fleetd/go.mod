module github.com/tmc/cove/cmd/cove-fleetd

go 1.25.5

require (
	github.com/tmc/cove v0.0.0
	github.com/tmc/cove-marina v0.0.0
)

require (
	github.com/beevik/etree v1.6.0 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/russellhaering/goxmldsig v1.6.0 // indirect
	github.com/tmc/cove-ocispec v0.0.0 // indirect
)

replace github.com/tmc/cove => ../..

replace github.com/tmc/cove-marina => ../../../cove-marina

replace github.com/tmc/cove-ocispec => ../../../cove-ocispec
