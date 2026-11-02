import Foundation

/// A redirect delegate's decision as a plain function: the request to follow a redirect
/// with, or nil (not followed). The delegate answers URLSession with it; a test stub asks it
/// whether a redirect will be followed before telling URLSession of one.
protocol RedirectRule: URLSessionTaskDelegate {
    func follow(from original: URLRequest?, to next: URLRequest) -> URLRequest?
}

/// A home's token goes only to the origin it was issued by. Every request that carries one
/// uses this as its task delegate: a redirect to the same origin keeps its headers; a
/// redirect to another origin (scheme, host or port) is followed without `Authorization`
/// (or the cookie), so a home, or something answering for it, cannot send the token on.
final class SameOriginRedirects: NSObject, RedirectRule, Sendable {
    static let shared = SameOriginRedirects()

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest) async -> URLRequest? {
        follow(from: task.originalRequest ?? task.currentRequest, to: request)
    }

    func follow(from original: URLRequest?, to next: URLRequest) -> URLRequest? {
        Self.redirect(from: original, to: next)
    }

    /// The request to follow a redirect with: as is to the same origin, else without credentials.
    static func redirect(from original: URLRequest?, to next: URLRequest) -> URLRequest {
        guard let from = original?.url, let to = next.url, sameOrigin(from, to) else {
            var stripped = next
            stripped.setValue(nil, forHTTPHeaderField: "Authorization")
            stripped.setValue(nil, forHTTPHeaderField: "Cookie")
            return stripped
        }
        return next
    }

    /// Same scheme, host (any case) and port (the scheme's default when none is written).
    static func sameOrigin(_ a: URL, _ b: URL) -> Bool {
        func origin(_ u: URL) -> (String, String, Int)? {
            guard let scheme = u.scheme?.lowercased(), let host = u.host()?.lowercased() else { return nil }
            return (scheme, host, u.port ?? (scheme == "https" ? 443 : scheme == "http" ? 80 : -1))
        }
        guard let x = origin(a), let y = origin(b) else { return false }
        return x == y
    }
}

/// For requests that carry a credential in their body (a password, a refresh token, a cloud
/// assertion) or are sent to Maktaba Cloud: a redirect is followed only to the same origin.
/// A 307 or 308 resends the body, so dropping the header would not be enough; to another
/// origin the redirect is not followed at all (the request ends with the 3xx answer).
final class SameOriginOnly: NSObject, RedirectRule, Sendable {
    static let shared = SameOriginOnly()

    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest) async -> URLRequest? {
        follow(from: task.originalRequest ?? task.currentRequest, to: request)
    }

    func follow(from original: URLRequest?, to next: URLRequest) -> URLRequest? {
        Self.redirect(from: original, to: next)
    }

    /// The request to follow a redirect with, or nil (not followed: another origin).
    static func redirect(from original: URLRequest?, to next: URLRequest) -> URLRequest? {
        guard let from = original?.url, let to = next.url, SameOriginRedirects.sameOrigin(from, to) else { return nil }
        return next
    }
}
