//! Reachability check for the configured server. Deliberately `std`-only —
//! a bare TCP connect is enough to decide "is something listening on that
//! host:port", and the SPA itself will surface any deeper (HTTP/auth)
//! failure once loaded. No HTTP client dependency needed for this.

use std::net::ToSocketAddrs;
use std::time::Duration;

use tauri::Url;

const CONNECT_TIMEOUT: Duration = Duration::from_millis(1500);

/// Resolves `server_url`'s host:port and attempts a TCP connect with a short
/// timeout. Returns `true` if a connection could be established (port open),
/// `false` for any parse error, resolution failure, refusal, or timeout.
pub fn is_reachable(server_url: &str) -> bool {
    let Ok(url) = Url::parse(server_url) else {
        return false;
    };
    let Some(host) = url.host_str() else {
        return false;
    };
    // url crate returns IPv6 hosts bracketed ("[::1]"); to_socket_addrs wants
    // the bare address.
    let host = host.trim_start_matches('[').trim_end_matches(']');
    let port = url.port_or_known_default().unwrap_or(80);

    let Ok(addrs) = (host, port).to_socket_addrs() else {
        return false;
    };
    // Try every resolved address, not just the first: a v4-only server behind a
    // dual-stack `localhost` that resolves ::1 first would otherwise read as
    // down. `any` short-circuits on the first success; a refused port fails
    // instantly, so only genuinely blackholed hosts pay the full timeout.
    addrs.into_iter().any(|addr| std::net::TcpStream::connect_timeout(&addr, CONNECT_TIMEOUT).is_ok())
}
