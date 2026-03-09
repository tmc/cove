import Foundation

public enum VZControlError: LocalizedError {
    case connectionFailed(String)
    case requestFailed(String)
    case timeout
    case invalidResponse
    case notConnected

    public var errorDescription: String? {
        switch self {
        case .connectionFailed(let msg): return "connection failed: \(msg)"
        case .requestFailed(let msg): return "request failed: \(msg)"
        case .timeout: return "operation timed out"
        case .invalidResponse: return "invalid response from server"
        case .notConnected: return "not connected to control socket"
        }
    }
}
