// protoflatten — codegen tool for Path C self-contained pubsub schemas.
//
// Reads a buf-built FileDescriptorSet (binpb) covering proto/, walks every
// per-aggregate event proto, and emits a SELF-CONTAINED .proto file per
// aggregate into proto/events-flat/. The flat proto inlines:
//   - chora.common.v1.EventEnvelope (from proto/chora/common/v1/envelope.proto)
//   - google.protobuf.Timestamp (well-known type, inlined as a local message)
//
// GCP Pub/Sub Schema Registry rejects schemas with `import` statements, so
// each registered schema must be self-contained. Source-of-truth remains at
// proto/events/ for Go/Python codegen via buf.
//
// Run from chora-contracts/ root:
//   go run ./internal/protoflatten -fds=/tmp/chora-fds.binpb -out=proto/events-flat
//
// Or via the wrapper script: scripts/flatten-pubsub-schemas.sh
//
// Standalone module so it does not pollute the chora-contracts/gen/go go.mod
// (which is the published codegen module path).

module github.com/apollo-chora/chora-contracts/internal/protoflatten

go 1.26.1

require google.golang.org/protobuf v1.36.11
