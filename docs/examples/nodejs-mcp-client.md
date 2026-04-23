---
title: Node.js MCP Client for cove
---
# Node.js MCP Client for cove

A minimal TypeScript program that connects to `cove serve --mcp` over stdio using the official Model Context Protocol SDK, lists running VMs, captures a screenshot, and runs a shell command inside the guest.

## Prerequisites

- Node 20 or newer
- npm (ships with Node)
- cove installed and on `$PATH` (`cove --version` should print a version)
- At least one cove VM registered and running (`cove up -user me` if you have none)

## package.json

Keep dependencies minimal. `tsx` runs TypeScript directly without a build step.

```json
{
  "name": "cove-mcp-example",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "dependencies": {
    "@modelcontextprotocol/sdk": "1.0.4"
  },
  "devDependencies": {
    "tsx": "^4.19.0",
    "typescript": "^5.5.0"
  }
}
```

Install with `npm install`.

## client.ts

```typescript
// client.ts -- Connect to `cove serve --mcp` and drive a VM over MCP.
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { writeFile } from "node:fs/promises";

// Shape of a cove VM entry returned by `vm_list`.
interface VMEntry {
  name: string;
  status: string;
}

interface VMListResponse {
  vms: VMEntry[];
}

// Helper: extract the first text content block from a tool response.
function firstText(result: { content: Array<{ type: string; text?: string }> }): string {
  const block = result.content.find((c) => c.type === "text");
  if (!block || !block.text) {
    throw new Error("tool response contained no text content");
  }
  return block.text;
}

// Helper: extract the first image content block from a tool response.
function firstImage(result: {
  content: Array<{ type: string; data?: string; mimeType?: string }>;
}): { data: string; mimeType: string } {
  const block = result.content.find((c) => c.type === "image");
  if (!block || !block.data) {
    throw new Error("tool response contained no image content");
  }
  return { data: block.data, mimeType: block.mimeType ?? "image/png" };
}

async function main(): Promise<void> {
  // 1. Spawn `cove serve --mcp` as a subprocess. The SDK wires stdin/stdout
  //    to the MCP framing layer; stderr flows to our terminal for debugging.
  const transport = new StdioClientTransport({
    command: "cove",
    args: ["serve", "--mcp"],
    stderr: "inherit",
  });

  // 2. Create the client with a name and version. These are advertised to
  //    the server during the initialize handshake.
  const client = new Client(
    { name: "cove-mcp-example", version: "0.1.0" },
    { capabilities: {} },
  );

  try {
    await client.connect(transport);
  } catch (err) {
    console.error("failed to connect to cove MCP server:", err);
    console.error("check that `cove --version` runs and a VM is registered.");
    process.exit(1);
  }

  try {
    // 3. List VMs. The tool returns a JSON string in a text block.
    const listResult = await client.callTool({ name: "vm_list", arguments: {} });
    const listBody: VMListResponse = JSON.parse(firstText(listResult));
    const vms = listBody.vms;

    if (vms.length === 0) {
      console.error("no VMs found. Create one with: cove up -user me");
      process.exit(2);
    }

    const target = vms[0];
    console.log(`found ${vms.length} VM(s); using '${target.name}' (status=${target.status})`);

    // 4. Take a screenshot. The tool returns MCP image content.
    const shotResult = await client.callTool({
      name: "vm_screenshot",
      arguments: { name: target.name },
    });
    const shot = firstImage(shotResult);
    const pngBytes = Buffer.from(shot.data, "base64");
    const shotPath = "/tmp/vm-shot.png";
    await writeFile(shotPath, pngBytes);
    console.log(`saved screenshot (${shot.mimeType}, ${pngBytes.length} bytes) to ${shotPath}`);

    // 5. Run `uname -a` inside the guest via the guest agent tool.
    const execResult = await client.callTool({
      name: "vm_agent_exec",
      arguments: { name: target.name, cmd: "uname", args: ["-a"] },
    });
    const execBody = JSON.parse(firstText(execResult));
    const execPayload = execBody.agentExecResult ?? {};
    console.log("uname -a output:");
    console.log((execPayload.stdout ?? execBody.data ?? "").trimEnd());
  } catch (err) {
    console.error("tool call failed:", err);
    process.exitCode = 3;
  } finally {
    // 6. Disconnect cleanly. This closes the subprocess stdio and reaps it.
    await client.close();
  }
}

main().catch((err) => {
  console.error("unhandled error:", err);
  process.exit(1);
});
```

## Run it

```bash
npm install
npx tsx client.ts
```

## Expected output

```
found 1 VM(s); using 'default' (status=running)
saved screenshot (image/png, 184213 bytes) to /tmp/vm-shot.png
uname -a output:
Darwin default 24.1.0 Darwin Kernel Version 24.1.0: ... arm64
```

Open `/tmp/vm-shot.png` to confirm the capture.

## Troubleshooting

- **`cove serve --mcp` exits immediately**: the `cove` binary is not on `$PATH`, or the MCP server hit an authentication error. Verify with `cove --version` and `cove list`. If needed, pass an absolute path via `command: "/usr/local/bin/cove"` in the transport options.
- **No VMs found**: the client reports zero entries from `vm_list`. Create one with `cove up -user me` or register an existing disk with `cove add`. Then retry.
- **SDK version mismatch**: MCP is young and the TypeScript SDK still makes breaking changes between minor versions. Pin `@modelcontextprotocol/sdk` to the exact version in `package.json` above rather than using a caret range. Bump deliberately and re-test.

## A note on MCP stability

The Model Context Protocol specification and its SDKs are still evolving. Tool names, argument shapes, and response envelopes can change between cove and SDK releases. This example pins the SDK version we have tested against; reproduce with the same pin before reporting issues.

## See also

- [MCP server feature guide](../features/mcp.md) -- tool inventory, resources, and server-side configuration.
- [HTTP control API reference](../reference/http-api.md) -- the REST endpoints that back the same tools, useful when Node is not available.
