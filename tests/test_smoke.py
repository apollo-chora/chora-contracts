from chora_contracts_gen.services import agent_executor_pb2, agent_executor_pb2_grpc

from chora_contracts import __version__


def test_package_metadata_and_generated_imports() -> None:
    assert __version__ == "2.1.0"
    request = agent_executor_pb2.ExecuteAgentRequest()
    assert request is not None
    assert agent_executor_pb2_grpc.AgentExecutorStub is not None
