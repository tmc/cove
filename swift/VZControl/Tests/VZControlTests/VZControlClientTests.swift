import XCTest
@testable import VZControl
import SwiftProtobuf

final class VZControlClientTests: XCTestCase {
    func testClientInit() {
        let client = VZControlClient(socketPath: "/tmp/test.sock")
        XCTAssertNotNil(client)
    }

    func testClientInitFromVMDir() {
        let client = VZControlClient(vmDir: "/tmp/nonexistent-vm")
        XCTAssertNotNil(client)
    }

    func testRequestSerialization() throws {
        var req = VZControl_ControlRequest()
        req.type = "ping"
        let data = try req.jsonUTF8Data()
        XCTAssertTrue(data.count > 0)

        let decoded = try VZControl_ControlRequest(jsonUTF8Data: data)
        XCTAssertEqual(decoded.type, "ping")
    }

    func testScreenshotRequestSerialization() throws {
        var req = VZControl_ControlRequest()
        req.type = "screenshot"
        var cmd = VZControl_ScreenshotCommand()
        cmd.scale = 0.5
        cmd.quality = 80
        cmd.format = "jpeg"
        req.screenshot = cmd

        let data = try req.jsonUTF8Data()
        let decoded = try VZControl_ControlRequest(jsonUTF8Data: data)
        XCTAssertEqual(decoded.type, "screenshot")
        XCTAssertEqual(decoded.screenshot.scale, 0.5)
        XCTAssertEqual(decoded.screenshot.quality, 80)
        XCTAssertEqual(decoded.screenshot.format, "jpeg")
    }

    func testKeyCommandSerialization() throws {
        var req = VZControl_ControlRequest()
        req.type = "key"
        var cmd = VZControl_KeyCommand()
        cmd.keyCode = 36
        cmd.keyDown = true
        cmd.useCgEvent = true
        req.key = cmd

        let data = try req.jsonUTF8Data()
        let decoded = try VZControl_ControlRequest(jsonUTF8Data: data)
        XCTAssertEqual(decoded.key.keyCode, 36)
        XCTAssertTrue(decoded.key.keyDown)
        XCTAssertTrue(decoded.key.useCgEvent)
    }

    func testResponseDeserialization() throws {
        let json = #"{"success":true,"data":"pong","message":{"message":"pong"}}"#
        let resp = try VZControl_ControlResponse(jsonUTF8Data: Data(json.utf8))
        XCTAssertTrue(resp.success)
        XCTAssertEqual(resp.data, "pong")
        XCTAssertEqual(resp.message.message, "pong")
    }

    func testStatusResponseDeserialization() throws {
        let json = #"{"success":true,"status":{"state":"running","canPause":true,"canResume":false,"canStop":true,"canRequestStop":true}}"#
        let resp = try VZControl_ControlResponse(jsonUTF8Data: Data(json.utf8))
        XCTAssertTrue(resp.success)
        XCTAssertEqual(resp.status.state, "running")
        XCTAssertTrue(resp.status.canPause)
        XCTAssertFalse(resp.status.canResume)
        XCTAssertTrue(resp.status.canStop)
    }

    func testCapabilitiesResponseDeserialization() throws {
        let json = #"{"success":true,"capabilities":{"protocolVersion":"1.0","encoding":"json","commands":["ping","status"],"features":{"ocr":true},"authRequired":false}}"#
        let resp = try VZControl_ControlResponse(jsonUTF8Data: Data(json.utf8))
        XCTAssertTrue(resp.success)
        XCTAssertEqual(resp.capabilities.protocolVersion, "1.0")
        XCTAssertEqual(resp.capabilities.commands, ["ping", "status"])
        XCTAssertEqual(resp.capabilities.features["ocr"], true)
    }

    func testErrorResponseDeserialization() throws {
        let json = #"{"success":false,"error":"vm not running"}"#
        let resp = try VZControl_ControlResponse(jsonUTF8Data: Data(json.utf8))
        XCTAssertFalse(resp.success)
        XCTAssertEqual(resp.error, "vm not running")
    }

    func testSnapshotListDeserialization() throws {
        let json = #"{"success":true,"snapshotList":{"snapshots":[{"name":"test","created":"2025-01-01T00:00:00Z","size":1024}]}}"#
        let resp = try VZControl_ControlResponse(jsonUTF8Data: Data(json.utf8))
        XCTAssertTrue(resp.success)
        XCTAssertEqual(resp.snapshotList.snapshots.count, 1)
        XCTAssertEqual(resp.snapshotList.snapshots[0].name, "test")
    }

    func testOCRTextDeserialization() throws {
        let json = #"{"success":true,"ocrText":{"text":"Hello World","matches":[{"text":"Hello","x":0.1,"y":0.2,"width":0.3,"height":0.05}]}}"#
        let resp = try VZControl_ControlResponse(jsonUTF8Data: Data(json.utf8))
        XCTAssertTrue(resp.success)
        XCTAssertEqual(resp.ocrText.text, "Hello World")
        XCTAssertEqual(resp.ocrText.matches.count, 1)
        XCTAssertEqual(resp.ocrText.matches[0].text, "Hello")
        XCTAssertEqual(resp.ocrText.matches[0].x, 0.1, accuracy: 0.001)
    }

    func testProcessInit() {
        let proc = VZMacosProcess(binaryPath: "/usr/local/bin/vz-macos")
        XCTAssertEqual(proc.binaryPath, "/usr/local/bin/vz-macos")
        XCTAssertFalse(proc.isRunning)
    }

    func testProcessClient() {
        let proc = VZMacosProcess(binaryPath: "/usr/local/bin/vz-macos", vmDir: "/tmp/test-vm")
        let client = proc.client()
        XCTAssertNotNil(client)
        XCTAssertEqual(proc.controlSocketPath, "/tmp/test-vm/control.sock")
    }

    func testVZControlErrorDescriptions() {
        let errors: [VZControlError] = [
            .connectionFailed("test"),
            .requestFailed("test"),
            .timeout,
            .invalidResponse,
            .notConnected,
        ]
        for err in errors {
            XCTAssertNotNil(err.errorDescription)
            XCTAssertFalse(err.errorDescription!.isEmpty)
        }
    }
}
