"""Gate LangChain tool calls through the Promtact inline gateway.

Each guarded tool consults Promtact before it runs: a denied or approval-required
call raises ToolBlocked instead of executing, so a compromised or misled agent
cannot reach a dangerous tool.

    from langchain_core.tools import Tool
    from promtact_gateway import PromtactGateway
    from langchain_guard import guard_tool

    gateway = PromtactGateway(url="http://127.0.0.1:8080", token="...")
    tools = [guard_tool(t, gateway) for t in raw_tools]
    # ... build your AgentExecutor with `tools` ...
"""

from __future__ import annotations

import json
from typing import Any

from langchain_core.tools import BaseTool, Tool

from promtact_gateway import PromtactGateway


def guard_tool(tool: BaseTool, gateway: PromtactGateway, allow_on_approval: bool = False) -> Tool:
    """Wrap a LangChain tool so Promtact gates every invocation before it runs.

    The gate is in the execution path (not a best-effort callback), so a blocked
    call never reaches the underlying tool. Single-input tools work as-is;
    structured tools should be wrapped with the same pattern on a StructuredTool.
    """

    def _guarded(tool_input: Any = "") -> Any:
        command = tool_input if isinstance(tool_input, str) else json.dumps(tool_input, default=str)
        gateway.enforce(tool.name, command=str(command), allow_on_approval=allow_on_approval)
        return tool.run(tool_input)

    return Tool(name=tool.name, description=tool.description, func=_guarded)
