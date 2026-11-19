import Foundation
import Network

/// True while the active network is Wi-Fi-like: a path that is satisfied, not expensive and not constrained (the iPhone's
/// reading of "unmetered", decision 0048, I3c). Everything automatic asks it before a byte moves; the requests also carry the
/// same rule as flags (invariant 4), so this is for the page and for the decisions, not the only guard.
nonisolated final class UnmeteredMonitor: @unchecked Sendable {
    static let shared = UnmeteredMonitor()

    private let monitor = NWPathMonitor()
    private let lock = NSLock()
    private var current = false

    private init() {
        monitor.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            lock.lock()
            current = Self.isUnmetered(path)
            lock.unlock()
        }
        monitor.start(queue: DispatchQueue(label: "app.tendshelf.ios.network"))
    }

    var value: Bool {
        lock.lock()
        defer { lock.unlock() }
        return current
    }

    static func isUnmetered(_ path: NWPath) -> Bool {
        path.status == .satisfied && !path.isExpensive && !path.isConstrained
    }
}
