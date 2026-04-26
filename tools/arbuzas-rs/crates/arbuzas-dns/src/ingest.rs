use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use anyhow::{Context, Result};
use arbuzas_dns_lib::state::{CanonicalDohEvent, CanonicalDotSession};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use tokio::task::JoinHandle;
use tracing::{info, warn};
use url::Url;

use crate::querylog_sync::{RecentDohHintRecord, RecentDotSessionRecord, SyncTriggerControl};
use crate::AppState;

const INGEST_BATCH_SIZE: usize = 256;
const INGEST_FLUSH_INTERVAL: Duration = Duration::from_millis(500);
const INGEST_LOCK_RETRY_DELAY: Duration = Duration::from_millis(100);

pub fn start_collectors(
    state: AppState,
    trigger: SyncTriggerControl,
) -> Result<Vec<JoinHandle<()>>> {
    let mut handles = Vec::new();
    handles.push(tokio::spawn(run_doh_listener(
        state.clone(),
        state.config.state_dir.join("doh-ingest.sock"),
        trigger.clone(),
    )));
    handles.push(tokio::spawn(run_dot_listener(
        state.clone(),
        state.config.state_dir.join("dot-ingest.sock"),
        trigger.clone(),
    )));
    handles.push(crate::querylog_sync::start_adguard_querylog_tail(
        state, trigger,
    ));
    Ok(handles)
}

async fn run_doh_listener(state: AppState, socket_path: PathBuf, trigger: SyncTriggerControl) {
    if let Err(error) = run_doh_listener_inner(state, &socket_path, trigger).await {
        warn!(
            "doh ingest listener stopped for {}: {error:#}",
            socket_path.display()
        );
    }
}

async fn run_dot_listener(state: AppState, socket_path: PathBuf, trigger: SyncTriggerControl) {
    if let Err(error) = run_dot_listener_inner(state, &socket_path, trigger).await {
        warn!(
            "dot ingest listener stopped for {}: {error:#}",
            socket_path.display()
        );
    }
}

async fn run_doh_listener_inner(
    state: AppState,
    socket_path: &Path,
    trigger: SyncTriggerControl,
) -> Result<()> {
    prepare_socket_path(socket_path)?;
    let socket = tokio::net::UnixDatagram::bind(socket_path)
        .with_context(|| format!("bind DoH ingest socket {}", socket_path.display()))?;
    fs::set_permissions(socket_path, fs::Permissions::from_mode(0o666)).with_context(|| {
        format!(
            "set permissions on DoH ingest socket {}",
            socket_path.display()
        )
    })?;
    info!("listening for DoH ingest on {}", socket_path.display());

    let mut batch = Vec::<DohEvent>::new();
    let mut buf = vec![0u8; 65_535];
    let mut last_flush = Instant::now();

    loop {
        let payload = match tokio::time::timeout(INGEST_FLUSH_INTERVAL, socket.recv(&mut buf)).await
        {
            Ok(Ok(len)) => Some(String::from_utf8_lossy(&buf[..len]).to_string()),
            Ok(Err(error)) => {
                return Err(error).with_context(|| {
                    format!("recv DoH ingest payload on {}", socket_path.display())
                });
            }
            Err(_) => None,
        };

        if let Some(message) = payload {
            let metadata = trigger
                .metadata_lookup(&state)
                .await
                .unwrap_or_else(|_| crate::querylog_sync::MetadataLookup::empty());
            if let Some(event) =
                parse_doh_ingest_message(&message, metadata.by_token(), now_epoch_seconds())
            {
                trigger
                    .record_doh_hints(&[RecentDohHintRecord {
                        identity_id: event.identity_id.clone(),
                        client_ip: event.client_ip.clone(),
                        row_time_ms: event.ts_ms,
                        request_time_ms: event.request_time_ms,
                        query_name: event.query_name.clone(),
                        query_type: event.query_type.clone(),
                    }])
                    .await;
                batch.push(event);
            }
        }

        if !batch.is_empty()
            && (batch.len() >= INGEST_BATCH_SIZE || last_flush.elapsed() >= INGEST_FLUSH_INTERVAL)
        {
            match flush_doh_events(&state, &batch).await {
                Ok(()) => {
                    trigger.notify_live_ingest();
                    batch.clear();
                    last_flush = Instant::now();
                }
                Err(error) if crate::querylog_sync::is_transient_sqlite_lock(&error) => {
                    warn!("doh ingest flush busy, retrying: {error:#}");
                    tokio::time::sleep(INGEST_LOCK_RETRY_DELAY).await;
                }
                Err(error) => return Err(error),
            }
        }
    }
}

async fn run_dot_listener_inner(
    state: AppState,
    socket_path: &Path,
    trigger: SyncTriggerControl,
) -> Result<()> {
    prepare_socket_path(socket_path)?;
    let socket = tokio::net::UnixDatagram::bind(socket_path)
        .with_context(|| format!("bind DoT ingest socket {}", socket_path.display()))?;
    fs::set_permissions(socket_path, fs::Permissions::from_mode(0o666)).with_context(|| {
        format!(
            "set permissions on DoT ingest socket {}",
            socket_path.display()
        )
    })?;
    info!("listening for DoT ingest on {}", socket_path.display());

    let mut batch = Vec::<DotSession>::new();
    let mut buf = vec![0u8; 65_535];
    let mut last_flush = Instant::now();

    loop {
        let payload = match tokio::time::timeout(INGEST_FLUSH_INTERVAL, socket.recv(&mut buf)).await
        {
            Ok(Ok(len)) => Some(String::from_utf8_lossy(&buf[..len]).to_string()),
            Ok(Err(error)) => {
                return Err(error).with_context(|| {
                    format!("recv DoT ingest payload on {}", socket_path.display())
                });
            }
            Err(_) => None,
        };

        if let Some(message) = payload {
            let metadata = trigger
                .metadata_lookup(&state)
                .await
                .unwrap_or_else(|_| crate::querylog_sync::MetadataLookup::empty());
            if let Some(session) = parse_dot_ingest_message(&message, metadata.by_dot_label()) {
                trigger
                    .record_dot_sessions(&[RecentDotSessionRecord {
                        identity_id: session.identity_id.clone(),
                        client_ip: session.client_ip.clone(),
                        server_name: session.server_name.clone(),
                        start_ms: session.start_ms,
                        end_ms: session.end_ms,
                        duration_ms: session.duration_ms,
                    }])
                    .await;
                batch.push(session);
            }
        }

        if !batch.is_empty()
            && (batch.len() >= INGEST_BATCH_SIZE || last_flush.elapsed() >= INGEST_FLUSH_INTERVAL)
        {
            match flush_dot_sessions(&state, &batch).await {
                Ok(()) => {
                    trigger.notify_live_ingest();
                    batch.clear();
                    last_flush = Instant::now();
                }
                Err(error) if crate::querylog_sync::is_transient_sqlite_lock(&error) => {
                    warn!("dot ingest flush busy, retrying: {error:#}");
                    tokio::time::sleep(INGEST_LOCK_RETRY_DELAY).await;
                }
                Err(error) => return Err(error),
            }
        }
    }
}

fn prepare_socket_path(path: &Path) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .with_context(|| format!("create socket directory {}", parent.display()))?;
    }
    match fs::remove_file(path) {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error).with_context(|| format!("remove {}", path.display())),
    }
    Ok(())
}

async fn flush_doh_events(state: &AppState, batch: &[DohEvent]) -> Result<()> {
    if batch.is_empty() {
        return Ok(());
    }
    let events = batch
        .iter()
        .map(|event| CanonicalDohEvent {
            ts_ms: event.ts_ms,
            identity_id: event.identity_id.clone(),
            client_ip: event.client_ip.clone(),
            status: event.status,
            request_time_ms: event.request_time_ms,
            query_name: event.query_name.clone(),
            query_type: event.query_type.clone(),
            protocol: event.protocol.clone(),
        })
        .collect::<Vec<_>>();
    state
        .write_serialized(|db| db.insert_doh_events(&events))
        .await
}

async fn flush_dot_sessions(state: &AppState, batch: &[DotSession]) -> Result<()> {
    if batch.is_empty() {
        return Ok(());
    }
    let sessions = batch
        .iter()
        .map(|session| CanonicalDotSession {
            end_ms: session.end_ms,
            identity_id: session.identity_id.clone(),
            client_ip: session.client_ip.clone(),
            start_ms: session.start_ms,
            duration_ms: session.duration_ms,
            server_name: session.server_name.clone(),
            status_code: session.status_code,
        })
        .collect::<Vec<_>>();
    state
        .write_serialized(|db| db.insert_dot_sessions(&sessions))
        .await
}

fn parse_doh_ingest_message(
    message: &str,
    token_map: &std::collections::BTreeMap<String, String>,
    fallback_epoch: i64,
) -> Option<DohEvent> {
    let payload = extract_ingest_payload(message);
    let parts = payload.split('\t').collect::<Vec<_>>();
    if parts.len() < 4 {
        return None;
    }
    let ts_raw = parts[0].trim();
    let uri_raw = parts[1].trim();
    let status_raw = parts[2].trim();
    let request_time_raw = parts[3].trim();
    let client_ip = parts
        .get(4)
        .map(|value| value.trim().to_string())
        .unwrap_or_default();
    let ts_ms_raw = parts.get(5).map(|value| value.trim()).unwrap_or_default();
    let status = status_raw.parse::<i64>().unwrap_or_default().max(0);
    let request_time_ms = parse_duration_millis(request_time_raw);
    let parsed_uri = parse_uri(uri_raw)?;
    let path = parsed_uri.path();
    let (query_name, query_type) = parse_doh_dns_query(&parsed_uri);
    let identity_id = if matches!(path, "/dns-query" | "/dns-query/") {
        "__bare__".to_string()
    } else if let Some(token) = token_from_doh_path(path) {
        token_map
            .get(token)
            .cloned()
            .unwrap_or_else(|| "__unknown__".to_string())
    } else {
        return None;
    };
    let epoch_ms = parse_iso8601_epoch_ms(ts_raw)
        .or_else(|| parse_epoch_seconds_millis(ts_ms_raw))
        .unwrap_or_else(|| fallback_epoch.max(0) * 1000);
    Some(DohEvent {
        ts_ms: epoch_ms.max(0),
        identity_id,
        client_ip,
        status,
        request_time_ms,
        query_name,
        query_type,
        protocol: "doh".to_string(),
    })
}

fn parse_dot_ingest_message(
    message: &str,
    dot_map: &std::collections::BTreeMap<String, String>,
) -> Option<DotSession> {
    let payload = extract_ingest_payload(message);
    let parts = payload.split('\t').collect::<Vec<_>>();
    if parts.len() < 5 {
        return None;
    }
    let ts_raw = parts[0].trim();
    let sni = parts[1].trim().to_ascii_lowercase();
    let status_raw = parts[2].trim();
    let session_time_raw = parts[3].trim();
    let client_ip = parts[4].trim().to_string();
    if sni.is_empty() || client_ip.is_empty() {
        return None;
    }
    let dot_label = sni
        .split('.')
        .next()
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    let identity_id = dot_map.get(&dot_label)?.clone();
    let end_ms = parts
        .get(5)
        .and_then(|value| parse_epoch_seconds_millis(value.trim()))
        .or_else(|| parse_iso8601_epoch_ms(ts_raw))?;
    let duration_ms = parse_duration_millis(session_time_raw);
    Some(DotSession {
        end_ms,
        identity_id,
        client_ip,
        start_ms: (end_ms - duration_ms).max(0),
        duration_ms,
        server_name: sni,
        status_code: status_raw.parse::<i64>().unwrap_or_default().max(0),
    })
}

fn extract_ingest_payload(text: &str) -> String {
    let bytes = text.as_bytes();
    for index in 0..bytes.len().saturating_sub(11) {
        if bytes[index..].len() < 11 {
            break;
        }
        if bytes[index].is_ascii_digit()
            && bytes[index + 1].is_ascii_digit()
            && bytes[index + 2].is_ascii_digit()
            && bytes[index + 3].is_ascii_digit()
            && bytes[index + 4] == b'-'
            && bytes[index + 5].is_ascii_digit()
            && bytes[index + 6].is_ascii_digit()
            && bytes[index + 7] == b'-'
            && bytes[index + 8].is_ascii_digit()
            && bytes[index + 9].is_ascii_digit()
            && bytes[index + 10] == b'T'
        {
            return text[index..].trim().to_string();
        }
    }
    text.trim().to_string()
}

fn parse_iso8601_epoch_ms(value: &str) -> Option<i64> {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return None;
    }
    let normalized = if trimmed.ends_with('Z') {
        format!("{}+00:00", &trimmed[..trimmed.len() - 1])
    } else {
        trimmed.to_string()
    };
    chrono::DateTime::<chrono::FixedOffset>::parse_from_rfc3339(&normalized)
        .ok()
        .map(|value| value.timestamp_millis())
}

fn parse_epoch_seconds_millis(value: &str) -> Option<i64> {
    value
        .trim()
        .parse::<f64>()
        .ok()
        .map(|value| (value * 1000.0).round() as i64)
}

fn parse_duration_millis(value: &str) -> i64 {
    value
        .trim()
        .parse::<f64>()
        .ok()
        .map(|value| (value * 1000.0).round() as i64)
        .unwrap_or_default()
        .max(0)
}

fn parse_uri(uri_raw: &str) -> Option<Url> {
    if uri_raw.trim().is_empty() {
        return None;
    }
    if uri_raw.starts_with("http://") || uri_raw.starts_with("https://") {
        Url::parse(uri_raw).ok()
    } else {
        Url::parse(&format!("http://localhost{uri_raw}")).ok()
    }
}

fn token_from_doh_path(path: &str) -> Option<&str> {
    let trimmed = path.trim_matches('/');
    let mut parts = trimmed.split('/');
    let token = parts.next()?;
    let dns_query = parts.next()?;
    if dns_query == "dns-query" && parts.next().is_none() && !token.is_empty() {
        Some(token)
    } else {
        None
    }
}

fn parse_doh_dns_query(parsed_uri: &Url) -> (String, String) {
    let encoded = parsed_uri
        .query_pairs()
        .find_map(|(key, value)| (key == "dns").then(|| value.into_owned()))
        .unwrap_or_default();
    if encoded.is_empty() {
        return (String::new(), String::new());
    }
    let Ok(payload) = URL_SAFE_NO_PAD.decode(encoded.as_bytes()) else {
        return (String::new(), String::new());
    };
    if payload.len() < 12 {
        return (String::new(), String::new());
    }
    let query_count = u16::from_be_bytes([payload[4], payload[5]]);
    if query_count == 0 {
        return (String::new(), String::new());
    }
    let mut offset = 12usize;
    let mut labels = Vec::<String>::new();
    while offset < payload.len() {
        let length = payload[offset] as usize;
        offset += 1;
        if length == 0 {
            break;
        }
        if (length & 0xC0) != 0 || offset + length > payload.len() {
            return (String::new(), String::new());
        }
        labels.push(
            String::from_utf8_lossy(&payload[offset..offset + length])
                .trim()
                .to_ascii_lowercase(),
        );
        offset += length;
    }
    if offset + 4 > payload.len() {
        return (String::new(), String::new());
    }
    let query_type = u16::from_be_bytes([payload[offset], payload[offset + 1]]);
    (labels.join("."), dns_query_type_label(query_type))
}

fn dns_query_type_label(query_type: u16) -> String {
    match query_type {
        1 => "A",
        2 => "NS",
        5 => "CNAME",
        6 => "SOA",
        12 => "PTR",
        15 => "MX",
        16 => "TXT",
        28 => "AAAA",
        33 => "SRV",
        64 => "SVCB",
        65 => "HTTPS",
        257 => "CAA",
        value => return value.to_string(),
    }
    .to_string()
}

fn now_epoch_seconds() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .unwrap_or_default()
}

#[derive(Debug, Clone)]
struct DohEvent {
    ts_ms: i64,
    identity_id: String,
    client_ip: String,
    status: i64,
    request_time_ms: i64,
    query_name: String,
    query_type: String,
    protocol: String,
}

#[derive(Debug, Clone)]
struct DotSession {
    end_ms: i64,
    identity_id: String,
    client_ip: String,
    start_ms: i64,
    duration_ms: i64,
    server_name: String,
    status_code: i64,
}
