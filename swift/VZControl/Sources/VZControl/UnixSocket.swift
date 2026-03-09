import Foundation

/// A simple AF_UNIX stream socket transport.
final class UnixSocket: @unchecked Sendable {
    private let fd: Int32
    private let bufferSize = 65536

    init(path: String) throws {
        fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            throw VZControlError.connectionFailed("socket() failed: \(String(cString: strerror(errno)))")
        }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = path.utf8
        guard pathBytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            Darwin.close(fd)
            throw VZControlError.connectionFailed("socket path too long")
        }
        withUnsafeMutablePointer(to: &addr.sun_path) { ptr in
            let raw = UnsafeMutableRawPointer(ptr)
            raw.copyMemory(from: Array(pathBytes) + [0], byteCount: pathBytes.count + 1)
        }

        let addrLen = socklen_t(MemoryLayout<sa_family_t>.size + pathBytes.count + 1)
        let result = withUnsafePointer(to: &addr) { addrPtr in
            addrPtr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockaddrPtr in
                Darwin.connect(fd, sockaddrPtr, addrLen)
            }
        }
        guard result == 0 else {
            let msg = String(cString: strerror(errno))
            Darwin.close(fd)
            throw VZControlError.connectionFailed("connect() failed: \(msg)")
        }
    }

    deinit {
        close()
    }

    func send(_ data: Data) throws {
        try data.withUnsafeBytes { buffer in
            guard let baseAddress = buffer.baseAddress else { return }
            var totalSent = 0
            while totalSent < data.count {
                let sent = Darwin.write(fd, baseAddress.advanced(by: totalSent), data.count - totalSent)
                guard sent > 0 else {
                    throw VZControlError.connectionFailed("write() failed: \(String(cString: strerror(errno)))")
                }
                totalSent += sent
            }
        }
    }

    /// Reads until a newline character is found. Returns the data including the newline.
    func readLine() throws -> Data {
        var result = Data()
        var buf = [UInt8](repeating: 0, count: bufferSize)
        while true {
            let n = Darwin.read(fd, &buf, bufferSize)
            if n < 0 {
                throw VZControlError.connectionFailed("read() failed: \(String(cString: strerror(errno)))")
            }
            if n == 0 {
                break
            }
            result.append(contentsOf: buf[0..<n])
            if buf[0..<n].contains(UInt8(ascii: "\n")) {
                break
            }
        }
        return result
    }

    func close() {
        Darwin.close(fd)
    }
}
