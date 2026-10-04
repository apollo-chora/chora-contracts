// schemaguard — CI guard: repo event protos vs the LIVE Pub/Sub committed schema revision.
//
// WHY THIS EXISTS (CHO-2155):
// GCP Pub/Sub's Schema Registry validator is STRICT. A binary proto message
// carrying a field that is NOT in the topic's committed schema revision is
// REJECTED AT PUBLISH with HTTP 400 "Message failed schema validation" —
// protobuf's usual unknown-field tolerance does NOT apply. The publish never
// lands, so it never dead-letters; it simply fails.
//
// The repo already guards source-proto -> flat-proto -> generated-Go via
// `make verify-reproducible`. The ONE link nothing guarded is:
//
//     proto/events-flat/**.proto   ->   the COMMITTED Pub/Sub schema revision
//
// That link was only ever closed by `terraform apply` on m10-data-plane — which
// this project has a standing rule never to run (TF state is ~228 resources
// behind; Pub/Sub is provisioned out-of-band). So the last mile of the schema
// pipeline is disconnected by policy, and protos drift ahead of the registry
// silently until a producer populates the new field and every publish 400s.
//
// Observed damage: chora.delivery.submission.graded.v1 published `hint_count`
// (field 12) against a committed revision that stopped at field 11. Because the
// encoder guarded the write with `if v != 0`, submissions with zero hints
// PASSED and submissions with hints were REJECTED — an intermittent,
// data-dependent outage that hid for weeks.
//
// Standalone module (mirrors internal/protoflatten) so it does not pollute
// chora-contracts/gen/go, which is the published codegen module path.
// Run with GOWORK=off.
module github.com/locoroco-git/Chora-LMS/chora-contracts/internal/schemaguard

go 1.26.1

require (
	google.golang.org/api v0.288.0
	google.golang.org/protobuf v1.36.11
)

require (
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.17 // indirect
	github.com/googleapis/gax-go/v2 v2.22.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.67.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260630182238-925bb5da69e7 // indirect
	google.golang.org/grpc v1.83.0 // indirect
)
