import Capacitor
import Foundation
import Network

/// Browses the local network for Podiom daemons (R8).
///
/// mDNS needs multicast sockets a WebView cannot open, so the browse runs here
/// and returns a plain list. It only ever reads: nothing is published, and no
/// connection is made beyond the one needed to turn a Bonjour service name into
/// an address the app can dial.
///
/// iOS gates all of this behind the local-network permission. The first call
/// triggers the system prompt, and Info.plist must carry both
/// NSLocalNetworkUsageDescription and NSBonjourServices listing `_podiom._tcp`
/// — without the latter the browse succeeds and silently returns nothing.
@objc(PodiomDiscoveryPlugin)
public class PodiomDiscoveryPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "PodiomDiscoveryPlugin"
    public let jsName = "PodiomDiscovery"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "discover", returnType: CAPPluginReturnPromise)
    ]

    private static let serviceType = "_podiom._tcp"
    private static let defaultTimeoutMs = 4000

    @objc func discover(_ call: CAPPluginCall) {
        let timeoutMs = call.getInt("timeoutMs") ?? Self.defaultTimeoutMs
        let session = BrowseSession(serviceType: Self.serviceType)
        session.run(timeout: .milliseconds(timeoutMs)) { instances in
            call.resolve(["instances": instances])
        }
    }
}

/// One browse, from start to timeout. mDNS has no "complete" signal — hosts
/// simply stop answering — so the session runs for a fixed window and reports
/// whatever resolved within it.
private final class BrowseSession {
    private let serviceType: String
    private let queue = DispatchQueue(label: "com.podiom.discovery")

    private var browser: NWBrowser?
    private var resolvers: [NWConnection] = []
    private var found: [String: [String: Any]] = [:]
    private var finished = false
    // Retained until the completion fires so ARC does not release the session
    // (and with it the browser) while the browse is still running.
    private var keepAlive: BrowseSession?

    init(serviceType: String) {
        self.serviceType = serviceType
    }

    func run(timeout: DispatchTimeInterval, completion: @escaping ([[String: Any]]) -> Void) {
        keepAlive = self

        let params = NWParameters()
        params.includePeerToPeer = false
        let descriptor = NWBrowser.Descriptor.bonjourWithTXTRecord(type: serviceType, domain: nil)
        let browser = NWBrowser(for: descriptor, using: params)
        self.browser = browser

        browser.browseResultsChangedHandler = { [weak self] results, _ in
            guard let self else { return }
            for result in results {
                self.resolve(result)
            }
        }

        browser.stateUpdateHandler = { [weak self] state in
            // A denied local-network permission surfaces as a failed browser.
            // Report an empty list rather than an error: the connection screen
            // must keep offering manual entry either way (R8).
            if case .failed = state {
                self?.finish(completion)
            }
        }

        browser.start(queue: queue)
        queue.asyncAfter(deadline: .now() + timeout) { [weak self] in
            self?.finish(completion)
        }
    }

    /// Turns a browse result into an address. The endpoint Bonjour hands back
    /// is a service name, which the WebView cannot dial, so a connection is
    /// opened purely to read the resolved host and port off its path.
    private func resolve(_ result: NWBrowser.Result) {
        guard case let .service(name, type, domain, _) = result.endpoint else { return }

        var version: String?
        if case let .bonjour(txt) = result.metadata {
            version = txt["version"]
        }

        let endpoint = NWEndpoint.service(name: name, type: type, domain: domain, interface: nil)
        let connection = NWConnection(to: endpoint, using: .tcp)
        resolvers.append(connection)

        connection.stateUpdateHandler = { [weak self] state in
            guard let self else { return }
            switch state {
            case .ready:
                if let inner = connection.currentPath?.remoteEndpoint,
                   case let .hostPort(host, port) = inner {
                    self.record(name: name, host: Self.describe(host), port: Int(port.rawValue), version: version)
                }
                connection.cancel()
            case .failed, .cancelled:
                connection.cancel()
            default:
                break
            }
        }
        connection.start(queue: queue)
    }

    private func record(name: String, host: String, port: Int, version: String?) {
        var instance: [String: Any] = ["name": name, "host": host, "port": port]
        if let version { instance["version"] = version }
        // Key on address so the same daemon seen over several interfaces (Wi-Fi
        // and a wired link, say) is offered once.
        found["\(host):\(port)"] = instance
    }

    /// Strips the zone suffix IPv6 link-local addresses carry ("fe80::1%en0").
    /// A URL cannot hold one, and any instance worth listing also answers on a
    /// routable address.
    private static func describe(_ host: NWEndpoint.Host) -> String {
        switch host {
        case let .ipv4(address):
            return "\(address)".components(separatedBy: "%").first ?? "\(address)"
        case let .ipv6(address):
            return "\(address)".components(separatedBy: "%").first ?? "\(address)"
        case let .name(name, _):
            return name
        @unknown default:
            return "\(host)"
        }
    }

    private func finish(_ completion: @escaping ([[String: Any]]) -> Void) {
        guard !finished else { return }
        finished = true

        browser?.cancel()
        browser = nil
        resolvers.forEach { $0.cancel() }
        resolvers.removeAll()

        let instances = Array(found.values)
        DispatchQueue.main.async {
            completion(instances)
            self.keepAlive = nil
        }
    }
}
