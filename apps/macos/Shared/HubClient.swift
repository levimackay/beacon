import Foundation

/// Where Beacon keeps its state on this machine. These paths mirror
/// internal/config/config.go; the app and the widget are clients of a hub
/// that already owns this directory, so neither ever creates anything here.
enum HubPaths {
    static var supportDirectory: URL {
        if let override = ProcessInfo.processInfo.environment["BEACON_DIR"], !override.isEmpty {
            return URL(fileURLWithPath: override, isDirectory: true)
        }
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home.appending(path: "Library/Application Support/Beacon", directoryHint: .isDirectory)
    }

    static var tokenFile: URL { supportDirectory.appending(path: "token") }

    /// The App Group the app and the widget share.
    ///
    /// The widget is sandboxed, which macOS requires before it will
    /// register a widget extension at all, so it cannot read the hub's
    /// support directory. The app is not sandboxed, because it must read
    /// the hub's token from that directory. The group container is the one
    /// place both can reach.
    static let appGroup = "VG4YGFQJCG.group.com.levimackay.beacon"

    /// The shared container, resolved through the sandbox when there is
    /// one and by path when there is not. An unsandboxed process gets no
    /// container from the entitlement lookup, so it addresses the same
    /// directory directly.
    static var sharedDirectory: URL {
        if let url = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup) {
            return url
        }
        return FileManager.default.homeDirectoryForCurrentUser
            .appending(path: "Library/Group Containers/\(appGroup)", directoryHint: .isDirectory)
    }

    /// The cached snapshot the app writes for the widget to read.
    ///
    /// Only the snapshot is shared, never the hub token. Copying the
    /// credential into a second location to let the widget poll on its own
    /// would widen where it can be read from in exchange for a refresh the
    /// app already performs, which is a bad trade for a monitoring tool
    /// whose whole security model rests on that one secret.
    static var cacheFile: URL { sharedDirectory.appending(path: "snapshot.json") }

    static var port: Int {
        if let raw = ProcessInfo.processInfo.environment["BEACON_PORT"], let p = Int(raw), p > 0, p < 65536 {
            return p
        }
        return 47654
    }
}

enum HubError: LocalizedError {
    case notConfigured
    case unreachable
    case unauthorized
    case badResponse(Int)

    var errorDescription: String? {
        switch self {
        case .notConfigured:
            "Beacon is not set up on this Mac yet."
        case .unreachable:
            "The Beacon hub is not responding."
        case .unauthorized:
            "The hub rejected this token."
        case .badResponse(let code):
            "The hub returned an unexpected response (\(code))."
        }
    }
}

/// A thin client over the hub's loopback API.
struct HubClient: Sendable {
    var baseURL: URL
    var token: String

    /// Builds a client from the token the hub wrote at first run. Returns
    /// nil when no hub has ever run on this machine: a client must never
    /// mint a credential the hub does not know about.
    init?() {
        guard let raw = try? String(contentsOf: HubPaths.tokenFile, encoding: .utf8) else { return nil }
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        guard let url = URL(string: "http://127.0.0.1:\(HubPaths.port)") else { return nil }
        self.baseURL = url
        self.token = trimmed
    }

    func snapshot() async throws -> Snapshot {
        var request = URLRequest(url: baseURL.appending(path: "v1/snapshot"))
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.timeoutInterval = 8
        // The hub is a local process whose data changes every few seconds;
        // a cached response would show the user stale health.
        request.cachePolicy = .reloadIgnoringLocalCacheData

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await URLSession.shared.data(for: request)
        } catch {
            throw HubError.unreachable
        }

        guard let http = response as? HTTPURLResponse else { throw HubError.unreachable }
        switch http.statusCode {
        case 200: break
        case 401: throw HubError.unauthorized
        default: throw HubError.badResponse(http.statusCode)
        }
        return try JSONDecoder.hub.decode(Snapshot.self, from: data)
    }
}
