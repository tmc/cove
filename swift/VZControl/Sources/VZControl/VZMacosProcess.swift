import Foundation

/// Manages the lifecycle of a vz-macos binary process.
public final class VZMacosProcess: @unchecked Sendable {
    public let binaryPath: String
    public let vmDir: String
    private var process: Process?

    public init(binaryPath: String, vmDir: String = "~/.vz/vms/default") {
        self.binaryPath = binaryPath
        self.vmDir = NSString(string: vmDir).expandingTildeInPath
    }

    public var controlSocketPath: String {
        (vmDir as NSString).appendingPathComponent("control.sock")
    }

    public var isRunning: Bool {
        process?.isRunning ?? false
    }

    /// Starts the vz-macos process with the given arguments.
    public func start(args: [String] = []) throws {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binaryPath)
        proc.arguments = args
        try proc.run()
        self.process = proc
    }

    /// Sends SIGTERM to the process.
    public func stop() {
        process?.terminate()
        process = nil
    }

    /// Returns a VZControlClient configured for this process's VM directory.
    public func client() -> VZControlClient {
        VZControlClient(vmDir: vmDir)
    }
}
