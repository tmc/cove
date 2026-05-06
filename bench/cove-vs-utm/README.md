# cove vs UTM benchmark

Compares cove and UTM on a boot-run-teardown workload. The UTM cell requires
the `utmctl` CLI to be installed on the host.

UTM does not expose cove's vsock guest agent. The protocol therefore records
the UTM control path separately, usually SSH or an existing guest tool.
