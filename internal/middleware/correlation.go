package middleware

// Header carries the correlation identifier across HTTP and WebSocket hops.
// The identifier itself is a transport-neutral context primitive and lives in
// internal/utils so packages below the middleware layer can read it.
const Header = "X-Kubeloop-Correlation-Id"
