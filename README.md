# chora-contracts

## About
chora-contracts is the contract repository for the Chora platform. It contains Protobuf definitions for events and gRPC services, AsyncAPI event specifications, OpenAPI REST specifications, GraphQL schemas, Meilisearch index definitions, and generated Go and Python bindings. The Python package also exposes the legacy `chora_contracts` package alongside the generated `chora_contracts_gen` bindings.

## Quick start
Prerequisites: Python 3.11 or newer, [uv](https://docs.astral.sh/uv/), and [Buf](https://buf.build/docs/installation/) for Protobuf linting and generation. Go 1.26.1 or newer is required when working with the generated Go module.

Clone the repository and install the Python package with its development dependencies:

```bash
git clone https://github.com/apollo-chora/chora-contracts.git
cd chora-contracts

uv sync --all-extras
```

Run the Python test suite:

```bash
uv run pytest tests/ -v
```

Generated bindings are committed to the repository. To regenerate them from the Protobuf sources:

```bash
make codegen
```

## Usage
The repository is organized by contract format:

- `proto/` contains the source-of-truth Protobuf definitions. Event schemas live under `proto/events/`, shared event types under `proto/common/`, and gRPC service contracts under `proto/services/`.
- `asyncapi/` contains AsyncAPI 3.0 event specifications, organized by domain.
- `openapi/` contains OpenAPI 3.2 REST specifications.
- `graphql/` contains the learner-facing GraphQL SDL files.
- `gen/go/` contains committed Go Protobuf and gRPC bindings. It is a Go module at `github.com/apollo-chora/chora-contracts/gen/go`.
- `gen/python/` contains committed Python Protobuf and gRPC bindings, packaged as `chora_contracts_gen`.
- `src/chora_contracts/` contains the legacy Pydantic package.

Go consumers can import generated service bindings directly from the Go module. For example:

```go
import executorv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/services/agent_executor/v1"
```

Then require the generated module in `go.mod`:

```text
require github.com/apollo-chora/chora-contracts/gen/go v0.0.0-...
```

Python consumers can install the repository and import generated modules from the stable `chora_contracts_gen` namespace:

```bash
pip install -e .
```

```python
from chora_contracts_gen.services import agent_executor_pb2_grpc
from chora_contracts_gen.chora.common.v1 import envelope_pb2
```

Event subjects follow the canonical form `chora.{domain}.{aggregate}.{event_type}.v{N}`. Event messages use `chora.common.v1.EventEnvelope` as field 1. Within a major schema version, changes are additive; breaking changes require a new major version.

## Development
The main build and validation commands are:

```bash
make lint
make breaking
make codegen
make flatten
make verify-reproducible
```

The Python CI workflow also runs:

```bash
uv sync --all-extras
uv run ruff check .
uv run ruff format --check .
uv run mypy src/
uv run pytest tests/ -v
```

Use `make codegen` after changing files under `proto/`. The target removes regenerated Python output, runs `buf generate proto`, then runs `scripts/relocate-flat-services.sh` to place flat service bindings in their declared Go package paths and rewrite Python imports under `chora_contracts_gen`.

Use `make flatten` to regenerate `proto/events-flat/`, the committed self-contained flattened event-schema artifact. `make verify-reproducible` runs full regeneration and fails if the committed generated trees differ from the generated output.

The generated trees are not edited by hand. CI checks the generated output and the flattened event schemas against their source definitions.

The repository also contains `schema-registry/committed-schemas.json` and `yaml/agent-guardrail-mapping.yaml` for contract-related data used by the project.
