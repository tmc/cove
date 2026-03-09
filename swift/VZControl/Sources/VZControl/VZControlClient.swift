import Foundation
import SwiftProtobuf

/// Async/await client for the vz-macos control socket protocol.
///
/// Each method opens a new AF_UNIX connection, sends a line-delimited ProtoJSON
/// request, reads a line-delimited ProtoJSON response, and closes the socket.
public final class VZControlClient: Sendable {
    private let socketPath: String
    private let authToken: String?

    public init(socketPath: String, authToken: String? = nil) {
        self.socketPath = socketPath
        self.authToken = authToken ?? ProcessInfo.processInfo.environment["VZ_MACOS_CTL_TOKEN"]
    }

    /// Convenience initializer that reads the auth token from the VM directory.
    public init(vmDir: String) {
        let expanded = NSString(string: vmDir).expandingTildeInPath
        self.socketPath = (expanded as NSString).appendingPathComponent("control.sock")
        let tokenPath = (expanded as NSString).appendingPathComponent("control.token")
        self.authToken = (try? String(contentsOfFile: tokenPath, encoding: .utf8))?
            .trimmingCharacters(in: .whitespacesAndNewlines)
            ?? ProcessInfo.processInfo.environment["VZ_MACOS_CTL_TOKEN"]
    }

    // MARK: - Transport

    private func send(_ request: VZControl_ControlRequest) async throws -> VZControl_ControlResponse {
        var req = request
        if let token = authToken, !token.isEmpty {
            req.authToken = token
        }

        let jsonData = try req.jsonUTF8Data()
        var payload = jsonData
        payload.append(UInt8(ascii: "\n"))

        let sock = try UnixSocket(path: socketPath)
        defer { sock.close() }

        try sock.send(payload)
        let responseData = try sock.readLine()

        guard !responseData.isEmpty else {
            throw VZControlError.invalidResponse
        }

        let trimmed = responseData.trimmingNewlines()
        let resp = try VZControl_ControlResponse(jsonUTF8Data: trimmed)

        if !resp.success {
            let msg = resp.error.isEmpty ? "unknown error" : resp.error
            throw VZControlError.requestFailed(msg)
        }

        return resp
    }

    // MARK: - Basic Commands

    public func ping() async throws {
        var req = VZControl_ControlRequest()
        req.type = "ping"
        _ = try await send(req)
    }

    public func status() async throws -> VZControl_StatusResponse {
        var req = VZControl_ControlRequest()
        req.type = "status"
        let resp = try await send(req)
        return resp.status
    }

    public func capabilities() async throws -> VZControl_CapabilitiesResponse {
        var req = VZControl_ControlRequest()
        req.type = "capabilities"
        let resp = try await send(req)
        return resp.capabilities
    }

    // MARK: - Screenshot

    public func screenshot(
        scale: Double = 0.5,
        quality: Int32 = 60,
        format: String = "jpeg"
    ) async throws -> VZControl_ScreenshotResponse {
        var req = VZControl_ControlRequest()
        req.type = "screenshot"
        var cmd = VZControl_ScreenshotCommand()
        cmd.scale = scale
        cmd.quality = quality
        cmd.format = format
        req.screenshot = cmd
        let resp = try await send(req)
        return resp.screenshotResult
    }

    // MARK: - Input

    public func typeText(_ text: String) async throws {
        var req = VZControl_ControlRequest()
        req.type = "type"
        var cmd = VZControl_TextCommand()
        cmd.text = text
        req.text = cmd
        _ = try await send(req)
    }

    public func keyPress(keyCode: UInt32, modifiers: UInt32 = 0) async throws {
        var req = VZControl_ControlRequest()
        req.type = "key"
        var cmd = VZControl_KeyCommand()
        cmd.keyCode = keyCode
        cmd.modifiers = modifiers
        cmd.keyDown = true
        cmd.useCgEvent = true
        req.key = cmd
        _ = try await send(req)
    }

    public func mouseClick(x: Double, y: Double) async throws {
        var req = VZControl_ControlRequest()
        req.type = "mouse"
        var cmd = VZControl_MouseCommand()
        cmd.x = x
        cmd.y = y
        cmd.button = 0
        cmd.action = "click"
        cmd.absolute = true
        req.mouse = cmd
        _ = try await send(req)
    }

    // MARK: - VM Control

    public func pause() async throws {
        var req = VZControl_ControlRequest()
        req.type = "pause"
        _ = try await send(req)
    }

    public func resume() async throws {
        var req = VZControl_ControlRequest()
        req.type = "resume"
        _ = try await send(req)
    }

    public func stop() async throws {
        var req = VZControl_ControlRequest()
        req.type = "stop"
        _ = try await send(req)
    }

    public func requestStop() async throws {
        var req = VZControl_ControlRequest()
        req.type = "request-stop"
        _ = try await send(req)
    }

    // MARK: - Networking

    public func networkInfo() async throws -> VZControl_NetworkInfoResponse {
        var req = VZControl_ControlRequest()
        req.type = "network-info"
        let resp = try await send(req)
        return resp.networkInfo
    }

    // MARK: - Memory

    public func memoryInfo() async throws -> VZControl_MemoryInfoResponse {
        var req = VZControl_ControlRequest()
        req.type = "memory"
        var cmd = VZControl_MemoryCommand()
        cmd.action = "info"
        req.memory = cmd
        let resp = try await send(req)
        return resp.memoryInfo
    }

    public func setMemory(sizeGB: Double) async throws {
        var req = VZControl_ControlRequest()
        req.type = "memory"
        var cmd = VZControl_MemoryCommand()
        cmd.action = "set"
        cmd.sizeGb = sizeGB
        req.memory = cmd
        _ = try await send(req)
    }

    // MARK: - Snapshots

    public func snapshotList() async throws -> VZControl_SnapshotListResponse {
        var req = VZControl_ControlRequest()
        req.type = "snapshot"
        var cmd = VZControl_SnapshotCommand()
        cmd.action = "list"
        req.snapshot = cmd
        let resp = try await send(req)
        return resp.snapshotList
    }

    public func snapshotSave(name: String) async throws -> String {
        var req = VZControl_ControlRequest()
        req.type = "snapshot"
        var cmd = VZControl_SnapshotCommand()
        cmd.action = "save"
        cmd.name = name
        req.snapshot = cmd
        let resp = try await send(req)
        return resp.snapshotAction.message
    }

    public func snapshotRestore(name: String) async throws -> String {
        var req = VZControl_ControlRequest()
        req.type = "snapshot"
        var cmd = VZControl_SnapshotCommand()
        cmd.action = "restore"
        cmd.name = name
        req.snapshot = cmd
        let resp = try await send(req)
        return resp.snapshotAction.message
    }

    public func snapshotDelete(name: String) async throws -> String {
        var req = VZControl_ControlRequest()
        req.type = "snapshot"
        var cmd = VZControl_SnapshotCommand()
        cmd.action = "delete"
        cmd.name = name
        req.snapshot = cmd
        let resp = try await send(req)
        return resp.snapshotAction.message
    }

    // MARK: - OCR

    public func ocrAllText() async throws -> String {
        var req = VZControl_ControlRequest()
        req.type = "ocr"
        var cmd = VZControl_OCRCommand()
        cmd.action = "all-text"
        req.ocr = cmd
        let resp = try await send(req)
        return resp.ocrText.text
    }

    public func ocrClickText(_ text: String, timeout: String = "10s") async throws {
        var req = VZControl_ControlRequest()
        req.type = "ocr"
        var cmd = VZControl_OCRCommand()
        cmd.action = "click"
        cmd.text = text
        cmd.timeout = timeout
        req.ocr = cmd
        _ = try await send(req)
    }

    // MARK: - Screen Detection

    public func detectScreen() async throws -> VZControl_ScreenDetectionResponse {
        var req = VZControl_ControlRequest()
        req.type = "ocr"
        var cmd = VZControl_OCRCommand()
        cmd.action = "detect-screen"
        req.ocr = cmd
        let resp = try await send(req)
        return resp.screenDetection
    }

    // MARK: - Agent

    public func agentExec(args: [String], env: [String: String] = [:], workingDir: String = "") async throws -> VZControl_AgentExecResponse {
        var req = VZControl_ControlRequest()
        req.type = "agent-exec"
        var cmd = VZControl_AgentExecCommand()
        cmd.args = args
        cmd.env = env
        cmd.workingDir = workingDir
        req.agentExec = cmd
        let resp = try await send(req)
        return resp.agentExecResult
    }

    public func agentRead(path: String) async throws -> VZControl_AgentFileResponse {
        var req = VZControl_ControlRequest()
        req.type = "agent-read"
        var cmd = VZControl_AgentFileReadCommand()
        cmd.path = path
        req.agentRead = cmd
        let resp = try await send(req)
        return resp.agentFile
    }

    public func agentWrite(path: String, data: String, mode: UInt32 = 0o644) async throws {
        var req = VZControl_ControlRequest()
        req.type = "agent-write"
        var cmd = VZControl_AgentFileWriteCommand()
        cmd.path = path
        cmd.data = data
        cmd.mode = mode
        req.agentWrite = cmd
        _ = try await send(req)
    }

    public func agentPing() async throws -> VZControl_AgentPingResponse {
        var req = VZControl_ControlRequest()
        req.type = "agent-ping"
        let resp = try await send(req)
        return resp.agentPing
    }

    public func agentShutdown(force: Bool = false) async throws {
        var req = VZControl_ControlRequest()
        req.type = "agent-shutdown"
        var cmd = VZControl_AgentShutdownCommand()
        cmd.force = force
        req.agentShutdown = cmd
        _ = try await send(req)
    }
}

// MARK: - Data helpers

private extension Data {
    func trimmingNewlines() -> Data {
        var end = count
        while end > 0 {
            let byte = self[end - 1]
            if byte == UInt8(ascii: "\n") || byte == UInt8(ascii: "\r") {
                end -= 1
            } else {
                break
            }
        }
        return self[0..<end]
    }
}
