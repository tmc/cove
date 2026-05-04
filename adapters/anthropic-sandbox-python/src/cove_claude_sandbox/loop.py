from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Sequence

from .actions import AnthropicToolDispatcher


BETA_HEADER = "computer-use-2025-11-24"
COMPUTER_TOOL = "computer_20251124"
BASH_TOOL = "bash_20250124"
TEXT_EDITOR_TOOL = "text_editor_20250728"


class AnthropicSandboxAPIError(RuntimeError):
    pass


class AnthropicSandboxLimitError(RuntimeError):
    pass


@dataclass(frozen=True)
class AnthropicSandboxResult:
    final_text: str
    messages: list[dict[str, Any]]
    iterations: int
    response: Any


class AnthropicAgentLoop:
    def __init__(
        self,
        *,
        anthropic_client: Any,
        dispatcher: AnthropicToolDispatcher,
        model: str,
        max_tokens: int = 4096,
        thinking: dict[str, Any] | None = None,
    ) -> None:
        self.anthropic_client = anthropic_client
        self.dispatcher = dispatcher
        self.model = model
        self.max_tokens = max_tokens
        self.thinking = thinking

    def run(
        self,
        prompt: str,
        *,
        max_iterations: int = 20,
        system: str | Sequence[dict[str, Any]] | None = None,
    ) -> AnthropicSandboxResult:
        messages: list[dict[str, Any]] = [{"role": "user", "content": prompt}]
        response: Any = None
        for iteration in range(1, max_iterations + 1):
            response = self._create(messages, system=system)
            assistant_content = [_block_to_dict(block) for block in _content(response)]
            messages.append({"role": "assistant", "content": assistant_content})
            tool_uses = [block for block in _content(response) if _block_field(block, "type") == "tool_use"]
            if not tool_uses:
                return AnthropicSandboxResult(
                    final_text=_text_from_blocks(_content(response)),
                    messages=messages,
                    iterations=iteration,
                    response=response,
                )
            messages.append(
                {
                    "role": "user",
                    "content": [self.dispatcher.dispatch(tool_use) for tool_use in tool_uses],
                }
            )
        raise AnthropicSandboxLimitError(f"max iterations exceeded: {max_iterations}")

    def _create(self, messages: list[dict[str, Any]], *, system: str | Sequence[dict[str, Any]] | None) -> Any:
        kwargs: dict[str, Any] = {
            "model": self.model,
            "max_tokens": self.max_tokens,
            "messages": messages,
            "tools": anthropic_tools(
                width=self.dispatcher.scaler.display_width,
                height=self.dispatcher.scaler.display_height,
            ),
            "betas": [BETA_HEADER],
        }
        if system is not None:
            kwargs["system"] = system
        if self.thinking is not None:
            kwargs["thinking"] = self.thinking
        try:
            return self.anthropic_client.beta.messages.create(**kwargs)
        except Exception as exc:  # noqa: BLE001
            raise AnthropicSandboxAPIError(f"anthropic messages create failed: {exc}") from exc


def anthropic_tools(*, width: int, height: int) -> list[dict[str, Any]]:
    return [
        {
            "type": COMPUTER_TOOL,
            "name": "computer",
            "display_width_px": width,
            "display_height_px": height,
            "display_number": 1,
        },
        {"type": TEXT_EDITOR_TOOL, "name": "str_replace_based_edit_tool"},
        {"type": BASH_TOOL, "name": "bash"},
    ]


def _content(response: Any) -> list[Any]:
    content = _block_field(response, "content")
    if isinstance(content, list):
        return content
    return []


def _block_field(block: Any, name: str) -> Any:
    if isinstance(block, dict):
        return block.get(name)
    return getattr(block, name, None)


def _block_to_dict(block: Any) -> dict[str, Any]:
    if isinstance(block, dict):
        return block
    if hasattr(block, "model_dump"):
        return block.model_dump()
    if hasattr(block, "dict"):
        return block.dict()
    fields = {}
    for name in ("type", "text", "id", "name", "input"):
        value = getattr(block, name, None)
        if value is not None:
            fields[name] = value
    return fields


def _text_from_blocks(blocks: list[Any]) -> str:
    texts = []
    for block in blocks:
        if _block_field(block, "type") == "text":
            texts.append(str(_block_field(block, "text") or ""))
    return "\n".join(text for text in texts if text)
