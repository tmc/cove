from __future__ import annotations

import copy

from cove_claude_sandbox.actions import AnthropicToolDispatcher
from cove_claude_sandbox.loop import BETA_HEADER, COMPUTER_TOOL, TEXT_EDITOR_TOOL, AnthropicAgentLoop


def test_loop_dispatches_tool_use_and_returns_final_text() -> None:
    client = _Anthropic(
        [
            _Response(
                [
                    {"type": "text", "text": "looking"},
                    {"type": "tool_use", "id": "tool-1", "name": "computer", "input": {"action": "screenshot"}},
                ]
            ),
            _Response([{"type": "text", "text": "done"}]),
        ]
    )
    dispatcher = AnthropicToolDispatcher(_Cove())
    result = AnthropicAgentLoop(anthropic_client=client, dispatcher=dispatcher, model="claude-opus-4-7").run("go")

    assert result.final_text == "done"
    assert result.iterations == 2
    assert client.calls[0]["betas"] == [BETA_HEADER]
    assert client.calls[0]["tools"][0]["type"] == COMPUTER_TOOL
    assert client.calls[0]["tools"][1]["type"] == TEXT_EDITOR_TOOL
    assert client.calls[1]["messages"][-1]["role"] == "user"
    tool_result = client.calls[1]["messages"][-1]["content"][0]
    assert tool_result["type"] == "tool_result"
    assert tool_result["tool_use_id"] == "tool-1"


class _Response:
    def __init__(self, content: list[dict[str, object]]) -> None:
        self.content = content


class _Anthropic:
    def __init__(self, responses: list[_Response]) -> None:
        self.responses = responses
        self.calls: list[dict[str, object]] = []
        self.beta = self
        self.messages = self

    def create(self, **kwargs: object) -> _Response:
        self.calls.append(copy.deepcopy(kwargs))
        return self.responses.pop(0)


class _Cove:
    def screenshot(self, **kwargs: object) -> bytes:
        del kwargs
        return b"png"
