import Foundation

enum CoveMountBridge {
    static func run(_ arguments: [String]) throws -> Data {
        let environment = ProcessInfo.processInfo.environment
        guard let helper = environment["COVE_MOUNT_HELPER"], !helper.isEmpty,
              let journal = environment["COVE_MOUNT_JOURNAL"], !journal.isEmpty else {
            throw NSError(domain: "cove.mount", code: 1, userInfo: [
                NSLocalizedDescriptionKey: "Cove mount helper and journal are required"
            ])
        }
        let process = Process()
        process.executableURL = URL(fileURLWithPath: helper)
        process.arguments = ["ios", "firmware", "_mount", "-journal", journal] + arguments
        let output = Pipe()
        process.standardOutput = output
        process.standardError = FileHandle.standardError
        try process.run()
        // Drain before waiting so a full pipe cannot block the helper's exit.
        let data = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw NSError(domain: "cove.mount", code: Int(process.terminationStatus), userInfo: [
                NSLocalizedDescriptionKey: "Cove mount helper failed"
            ])
        }
        return data
    }
}
