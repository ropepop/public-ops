use std::collections::BTreeMap;
use std::fs;
use std::io::{self, BufRead, BufReader, Cursor};
use std::net::{IpAddr, SocketAddr};
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant};

use anyhow::{anyhow, bail, Context, Result};
use arbuzas_dns_lib::config::{write_atomic, RuntimeConfig};
use arbuzas_dns_lib::state::{
    now_epoch_millis, now_iso8601, now_iso8601_millis, ControlPlaneDb, ControlPlaneDbWriter,
};
use axum::body::Bytes;
use axum::extract::OriginalUri;
use axum::http::header::{ACCEPT, CONTENT_TYPE};
use axum::http::{HeaderMap, HeaderValue, Method, StatusCode};
use axum::response::{IntoResponse, Response};
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use hickory_proto::op::{Message, ResponseCode};
use hickory_proto::rr::rdata::{A, AAAA};
use hickory_proto::rr::{RData, Record, RecordType};
use hickory_proto::serialize::binary::BinEncodable;
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream, UdpSocket};
use tokio::sync::{mpsc, Mutex};
use tokio::task::JoinHandle;
use tokio_rustls::rustls;
use tokio_rustls::rustls::pki_types::{CertificateDer, PrivateKeyDer};
use tokio_rustls::TlsAcceptor;
use tracing::{info, warn};

use crate::querylog_sync::{build_live_write_row, LiveMirrorSeed};
use crate::AppState;

const DNS_CACHE_MAX_ENTRIES: usize = 4096;
const DNS_QUERY_WRITER_CAPACITY: usize = 8192;
const DNS_QUERY_WRITER_BATCH_SIZE: usize = 256;
const DNS_QUERY_WRITER_FLUSH_INTERVAL: Duration = Duration::from_millis(250);
const DNS_QUERY_SEARCH_INDEX_INTERVAL: Duration = Duration::from_secs(10);
const DNS_QUERY_ARCHIVE_INTERVAL: Duration = Duration::from_secs(15 * 60);
const DNS_QUERY_SEARCH_INDEX_BATCH_SIZE: usize = 2048;
const DNS_QUERY_ARCHIVE_MAX_SHARDS: usize = 1;
const BLOCKED_TTL_SECONDS: u32 = 60;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct CompiledPolicy {
    pub schema_version: i64,
    pub compiled_at: String,
    pub source_hash: String,
    pub compiled_hash: String,
    pub upstreams: Vec<String>,
    pub filter_lookup: BTreeMap<i64, String>,
    pub allow_exact: BTreeMap<String, CompiledRuleSource>,
    pub allow_suffix: BTreeMap<String, CompiledRuleSource>,
    pub block_exact: BTreeMap<String, CompiledRuleSource>,
    pub block_suffix: BTreeMap<String, CompiledRuleSource>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct CompiledRuleSource {
    pub filter_id: Option<i64>,
    pub label: String,
    pub rule_text: String,
}

#[derive(Debug, Clone)]
pub(crate) enum PolicyDecision {
    Allow,
    Block(CompiledRuleSource),
}

#[derive(Debug, Clone, Default)]
pub(crate) struct DnsResponseCache {
    inner: Arc<Mutex<BTreeMap<String, CachedDnsResponse>>>,
}

#[derive(Debug, Clone)]
struct CachedDnsResponse {
    response_bytes: Vec<u8>,
    expires_at: Instant,
    last_accessed_at: Instant,
}

#[derive(Debug, Clone)]
pub(crate) struct NativeQueryWriterControl {
    sender: mpsc::Sender<NativeQueryEvent>,
}

#[derive(Debug, Clone)]
pub(crate) struct NativeQueryEvent {
    row: arbuzas_dns_lib::state::QuerylogMirrorWriteRow,
}

#[derive(Debug, Clone)]
pub(crate) struct PreparedCompiledPolicyWrite {
    pub source_bytes: Vec<u8>,
    pub compiled: CompiledPolicy,
    pub compiled_bytes: Vec<u8>,
}

impl PreparedCompiledPolicyWrite {
    pub(crate) fn response_payload(&self, config: &RuntimeConfig) -> Value {
        json!({
            "ok": true,
            "publisher": "native",
            "sourceConfigFile": config.source_config_file.display().to_string(),
            "compiledPolicyFile": config.compiled_policy_file.display().to_string(),
            "sourceConfigHash": self.compiled.source_hash.clone(),
            "managedPolicyHash": self.compiled.compiled_hash.clone(),
            "upstreamCount": self.compiled.upstreams.len(),
            "allowExactCount": self.compiled.allow_exact.len(),
            "allowSuffixCount": self.compiled.allow_suffix.len(),
            "blockExactCount": self.compiled.block_exact.len(),
            "blockSuffixCount": self.compiled.block_suffix.len(),
        })
    }
}

#[derive(Debug)]
struct DnsQueryResult {
    response_bytes: Vec<u8>,
    query_name: String,
    query_type: String,
    row_time: String,
    row_time_ms: i64,
    status_raw: String,
    cached: bool,
    upstream: String,
    elapsed_ms: i64,
    filter_id: Option<i64>,
    rule_text: String,
    reason: String,
}

#[derive(Debug, Clone, Copy)]
enum NativeProtocol {
    Dns,
    Doh,
    Dot,
}

impl NativeProtocol {
    fn as_querylog_proto(self) -> &'static str {
        match self {
            Self::Dns => "dns",
            Self::Doh => "doh",
            Self::Dot => "dot",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct SourceConfig {
    #[serde(default)]
    pub schema_version: Option<i64>,
    #[serde(default)]
    pub upstreams: Vec<String>,
    #[serde(default)]
    pub filters: Vec<SourceFilterEntry>,
    #[serde(default)]
    pub whitelist_filters: Vec<SourceFilterEntry>,
    #[serde(default)]
    pub user_rules: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub(crate) struct SourceFilterEntry {
    pub url: String,
    pub name: String,
    #[serde(default = "default_enabled_flag")]
    pub enabled: serde_yaml::Value,
    #[serde(default)]
    pub id: Option<i64>,
}

fn default_enabled_flag() -> serde_yaml::Value {
    serde_yaml::Value::Bool(true)
}

impl CompiledPolicy {
    pub(crate) fn decide(&self, query_name: &str) -> PolicyDecision {
        let normalized = normalize_domain(query_name);
        if normalized.is_empty() {
            return PolicyDecision::Allow;
        }

        if let Some(source) = self
            .allow_exact
            .get(&normalized)
            .cloned()
            .or_else(|| match_suffix_rule(&self.allow_suffix, &normalized))
        {
            let _ = source;
            return PolicyDecision::Allow;
        }

        if let Some(source) = self
            .block_exact
            .get(&normalized)
            .cloned()
            .or_else(|| match_suffix_rule(&self.block_suffix, &normalized))
        {
            return PolicyDecision::Block(source);
        }

        PolicyDecision::Allow
    }

    fn primary_upstream(&self) -> Result<&str> {
        self.upstreams
            .first()
            .map(String::as_str)
            .filter(|value| !value.trim().is_empty())
            .ok_or_else(|| anyhow!("compiled policy does not include any upstreams"))
    }
}

impl DnsResponseCache {
    pub(crate) async fn get(&self, key: &str, message_id: u16) -> Option<Vec<u8>> {
        let mut guard = self.inner.lock().await;
        let now = Instant::now();
        let Some(entry) = guard.get_mut(key) else {
            return None;
        };
        if entry.expires_at <= now {
            guard.remove(key);
            return None;
        }
        entry.last_accessed_at = now;
        let mut message = Message::from_vec(&entry.response_bytes).ok()?;
        message.set_id(message_id);
        message.to_bytes().ok()
    }

    pub(crate) async fn insert(&self, key: String, response_bytes: Vec<u8>, ttl_seconds: u32) {
        if ttl_seconds == 0 {
            return;
        }
        let mut guard = self.inner.lock().await;
        guard.insert(
            key,
            CachedDnsResponse {
                response_bytes,
                expires_at: Instant::now() + Duration::from_secs(u64::from(ttl_seconds)),
                last_accessed_at: Instant::now(),
            },
        );
        while guard.len() > DNS_CACHE_MAX_ENTRIES {
            let Some(oldest_key) = guard
                .iter()
                .min_by_key(|(_, value)| value.last_accessed_at)
                .map(|(key, _)| key.clone())
            else {
                break;
            };
            guard.remove(&oldest_key);
        }
    }
}

impl NativeQueryWriterControl {
    pub(crate) async fn send(&self, event: NativeQueryEvent) {
        let _ = self.sender.send(event).await;
    }
}

pub(crate) async fn prepare_compiled_policy(
    config: &RuntimeConfig,
    client: &reqwest::Client,
) -> Result<Value> {
    let raw = fs::read_to_string(&config.source_config_file)
        .with_context(|| format!("read {}", config.source_config_file.display()))?;
    let source = parse_source_config_text(&raw, &config.source_config_file.display().to_string())?;
    let prepared = prepare_compiled_policy_write(&source, client).await?;
    persist_prepared_compiled_policy(config, &prepared)?;
    Ok(prepared.response_payload(config))
}

pub(crate) fn load_compiled_policy(config: &RuntimeConfig) -> Result<Arc<CompiledPolicy>> {
    let raw = fs::read(&config.compiled_policy_file)
        .with_context(|| format!("read {}", config.compiled_policy_file.display()))?;
    let policy: CompiledPolicy = serde_json::from_slice(&raw)
        .with_context(|| format!("parse {}", config.compiled_policy_file.display()))?;
    Ok(Arc::new(policy))
}

pub(crate) fn start_query_writer(
    config: &RuntimeConfig,
) -> (NativeQueryWriterControl, Vec<JoinHandle<()>>) {
    let (sender, receiver) = mpsc::channel(DNS_QUERY_WRITER_CAPACITY);
    let control = NativeQueryWriterControl { sender };
    let writer_config = config.clone();
    let search_config = config.clone();
    let archive_config = config.clone();
    let tasks = vec![
        tokio::spawn(async move {
            run_query_writer(writer_config, receiver).await;
        }),
        tokio::spawn(async move {
            run_querylog_search_queue_worker(search_config).await;
        }),
        tokio::spawn(async move {
            run_querylog_archive_worker(archive_config).await;
        }),
    ];
    (control, tasks)
}

pub(crate) fn start_dns_listeners(state: AppState) -> Result<Vec<JoinHandle<()>>> {
    let bind_addr = SocketAddr::new(
        state
            .config
            .dns_bind_host
            .parse::<IpAddr>()
            .unwrap_or(IpAddr::from([0, 0, 0, 0])),
        state.config.dns_port,
    );
    let mut tasks = Vec::new();
    tasks.push(tokio::spawn(run_udp_listener(state.clone(), bind_addr)));
    tasks.push(tokio::spawn(run_tcp_listener(state.clone(), bind_addr)));
    if state.config.dot_enabled {
        let dot_addr = SocketAddr::new(bind_addr.ip(), state.config.dot_port);
        tasks.push(tokio::spawn(run_dot_listener(state, dot_addr)));
    }
    Ok(tasks)
}

pub(crate) fn doh_path_matches(path: &str) -> bool {
    extract_doh_token(path).is_some()
}

pub(crate) async fn handle_doh_request(
    state: &AppState,
    method: &Method,
    headers: &HeaderMap,
    uri: &OriginalUri,
    body: Bytes,
    remote_addr: Option<SocketAddr>,
) -> Result<Response> {
    let path = uri.path();
    let Some(token) = extract_doh_token(path) else {
        bail!("not a DoH path");
    };
    if *method != Method::GET && *method != Method::POST && *method != Method::HEAD {
        return Ok(StatusCode::METHOD_NOT_ALLOWED.into_response());
    }

    let dns_payload = match *method {
        Method::GET | Method::HEAD => {
            let dns_param = uri
                .query()
                .and_then(extract_dns_query_param)
                .ok_or_else(|| anyhow!("missing dns query parameter"))?;
            URL_SAFE_NO_PAD
                .decode(dns_param.as_bytes())
                .or_else(|_| base64::engine::general_purpose::URL_SAFE.decode(dns_param.as_bytes()))
                .context("decode dns query parameter")?
        }
        Method::POST => body.to_vec(),
        _ => Vec::new(),
    };

    let client_ip = extract_client_ip(headers, remote_addr)
        .map(|value| value.to_string())
        .unwrap_or_default();
    let metadata = state.sync_trigger.metadata_lookup(state).await?;
    let identity_id = resolve_doh_identity(&token, &metadata);
    let resolved = resolve_query(
        state,
        &dns_payload,
        NativeProtocol::Doh,
        &identity_id,
        &client_ip,
        "",
    )
    .await?;
    enqueue_live_query(
        state,
        NativeProtocol::Doh,
        &identity_id,
        &client_ip,
        "",
        None,
        &resolved,
    )
    .await;

    if *method == Method::HEAD {
        let mut response = Response::new(axum::body::Body::empty());
        *response.status_mut() = StatusCode::OK;
        response.headers_mut().insert(
            CONTENT_TYPE,
            HeaderValue::from_static("application/dns-message"),
        );
        return Ok(response);
    }

    let mut response = Response::new(axum::body::Body::from(resolved.response_bytes));
    *response.status_mut() = StatusCode::OK;
    response.headers_mut().insert(
        CONTENT_TYPE,
        HeaderValue::from_static("application/dns-message"),
    );
    Ok(response)
}

fn extract_doh_token(path: &str) -> Option<Option<String>> {
    let trimmed = path.trim();
    if trimmed == "/dns-query" || trimmed == "/dns-query/" {
        return Some(None);
    }
    let mut segments = trimmed.trim_matches('/').split('/').collect::<Vec<_>>();
    if segments.len() == 2 && segments[1] == "dns-query" && !segments[0].trim().is_empty() {
        return Some(Some(segments.remove(0).to_string()));
    }
    None
}

async fn run_udp_listener(state: AppState, bind_addr: SocketAddr) {
    let Ok(socket) = UdpSocket::bind(bind_addr).await else {
        warn!("failed to bind UDP DNS listener on {bind_addr}");
        return;
    };
    info!("native UDP DNS listening on {bind_addr}");
    let socket = Arc::new(socket);
    let mut buf = vec![0u8; 4096];
    loop {
        let Ok((len, peer)) = socket.recv_from(&mut buf).await else {
            continue;
        };
        let query = buf[..len].to_vec();
        let socket = socket.clone();
        let state = state.clone();
        tokio::spawn(async move {
            let client_ip = peer.ip().to_string();
            match resolve_query(
                &state,
                &query,
                NativeProtocol::Dns,
                "__bare__",
                &client_ip,
                "",
            )
            .await
            {
                Ok(result) => {
                    enqueue_live_query(
                        &state,
                        NativeProtocol::Dns,
                        "__bare__",
                        &client_ip,
                        "",
                        None,
                        &result,
                    )
                    .await;
                    let _ = socket.send_to(&result.response_bytes, peer).await;
                }
                Err(error) => {
                    warn!("dns udp query failed from {peer}: {error:#}");
                }
            }
        });
    }
}

async fn run_tcp_listener(state: AppState, bind_addr: SocketAddr) {
    let Ok(listener) = TcpListener::bind(bind_addr).await else {
        warn!("failed to bind TCP DNS listener on {bind_addr}");
        return;
    };
    info!("native TCP DNS listening on {bind_addr}");
    loop {
        let Ok((stream, peer)) = listener.accept().await else {
            continue;
        };
        let state = state.clone();
        tokio::spawn(async move {
            if let Err(error) = handle_tcp_connection(
                state,
                stream,
                peer.ip().to_string(),
                NativeProtocol::Dns,
                "__bare__",
                None,
            )
            .await
            {
                warn!("dns tcp connection failed from {peer}: {error:#}");
            }
        });
    }
}

async fn run_dot_listener(state: AppState, bind_addr: SocketAddr) {
    let Ok(listener) = TcpListener::bind(bind_addr).await else {
        warn!("failed to bind DoT listener on {bind_addr}");
        return;
    };
    let Ok(acceptor) = tls_acceptor(&state.config) else {
        warn!("failed to load DoT TLS config");
        return;
    };
    info!("native DoT listening on {bind_addr}");
    loop {
        let Ok((stream, peer)) = listener.accept().await else {
            continue;
        };
        let state = state.clone();
        let acceptor = acceptor.clone();
        tokio::spawn(async move {
            match acceptor.accept(stream).await {
                Ok(tls_stream) => {
                    let server_name = dot_server_name(&tls_stream);
                    let metadata = state
                        .sync_trigger
                        .metadata_lookup(&state)
                        .await
                        .unwrap_or_else(|_| crate::querylog_sync::MetadataLookup::empty());
                    let identity_id = resolve_dot_identity(server_name.as_deref(), &metadata);
                    if let Err(error) = handle_dot_connection(
                        state,
                        tls_stream,
                        peer.ip().to_string(),
                        identity_id,
                        server_name,
                    )
                    .await
                    {
                        warn!("dot connection failed from {peer}: {error:#}");
                    }
                }
                Err(error) => {
                    warn!("dot tls handshake failed from {peer}: {error:#}");
                }
            }
        });
    }
}

async fn handle_dot_connection(
    state: AppState,
    mut stream: tokio_rustls::server::TlsStream<TcpStream>,
    client_ip: String,
    identity_id: String,
    server_name: Option<String>,
) -> Result<()> {
    loop {
        let mut len_buf = [0u8; 2];
        if stream.read_exact(&mut len_buf).await.is_err() {
            return Ok(());
        }
        let size = usize::from(u16::from_be_bytes(len_buf));
        if size == 0 {
            continue;
        }
        let mut query = vec![0u8; size];
        stream.read_exact(&mut query).await?;
        let result = resolve_query(
            &state,
            &query,
            NativeProtocol::Dot,
            &identity_id,
            &client_ip,
            server_name.as_deref().unwrap_or_default(),
        )
        .await?;
        enqueue_live_query(
            &state,
            NativeProtocol::Dot,
            &identity_id,
            &client_ip,
            "",
            server_name.as_deref(),
            &result,
        )
        .await;
        let response_len = u16::try_from(result.response_bytes.len())
            .context("dns response too large for tcp framing")?;
        stream.write_all(&response_len.to_be_bytes()).await?;
        stream.write_all(&result.response_bytes).await?;
        stream.flush().await?;
    }
}

async fn handle_tcp_connection(
    state: AppState,
    mut stream: TcpStream,
    client_ip: String,
    protocol: NativeProtocol,
    identity_id: &str,
    server_name: Option<&str>,
) -> Result<()> {
    loop {
        let mut len_buf = [0u8; 2];
        if stream.read_exact(&mut len_buf).await.is_err() {
            return Ok(());
        }
        let size = usize::from(u16::from_be_bytes(len_buf));
        if size == 0 {
            continue;
        }
        let mut query = vec![0u8; size];
        stream.read_exact(&mut query).await?;
        let result = resolve_query(
            &state,
            &query,
            protocol,
            identity_id,
            &client_ip,
            server_name.unwrap_or_default(),
        )
        .await?;
        enqueue_live_query(
            &state,
            protocol,
            identity_id,
            &client_ip,
            "",
            server_name,
            &result,
        )
        .await;
        let response_len = u16::try_from(result.response_bytes.len())
            .context("dns response too large for tcp framing")?;
        stream.write_all(&response_len.to_be_bytes()).await?;
        stream.write_all(&result.response_bytes).await?;
        stream.flush().await?;
    }
}

async fn resolve_query(
    state: &AppState,
    query_bytes: &[u8],
    _protocol: NativeProtocol,
    identity_id: &str,
    client_ip: &str,
    server_name: &str,
) -> Result<DnsQueryResult> {
    let request = Message::from_vec(query_bytes).context("parse dns query")?;
    let query = request
        .query()
        .ok_or_else(|| anyhow!("dns query is missing question section"))?;
    let query_name = normalize_domain(&query.name().to_string());
    let query_type = query.query_type().to_string();
    let row_time = now_iso8601_millis();
    let row_time_ms = now_epoch_millis();

    let compiled_policy = state.compiled_policy();
    match compiled_policy.decide(&query_name) {
        PolicyDecision::Block(source) => {
            let response_bytes = build_blocked_response(&request, &query_name, query.query_type())?;
            return Ok(DnsQueryResult {
                response_bytes,
                query_name,
                query_type,
                row_time,
                row_time_ms,
                status_raw: "NXDOMAIN".to_string(),
                cached: false,
                upstream: "policy:block".to_string(),
                elapsed_ms: 0,
                filter_id: source.filter_id,
                rule_text: source.rule_text,
                reason: "BlockedByPolicy".to_string(),
            });
        }
        PolicyDecision::Allow => {}
    }

    let cache_key = cache_key_for_query(&query_name, &query_type);
    if let Some(response_bytes) = state.dns_cache.get(&cache_key, request.id()).await {
        return Ok(DnsQueryResult {
            response_bytes,
            query_name,
            query_type,
            row_time,
            row_time_ms,
            status_raw: "NOERROR".to_string(),
            cached: true,
            upstream: "cache".to_string(),
            elapsed_ms: 0,
            filter_id: None,
            rule_text: String::new(),
            reason: "CacheHit".to_string(),
        });
    }

    let started = Instant::now();
    let upstream = compiled_policy.primary_upstream()?.to_string();
    let response_bytes = forward_query_to_doh(&state.client, &upstream, query_bytes).await?;
    let elapsed_ms = i64::try_from(started.elapsed().as_millis()).unwrap_or_default();
    let response = Message::from_vec(&response_bytes).context("parse dns response")?;
    let status_raw = response.response_code().to_string();
    let ttl = minimum_ttl(&response).unwrap_or(0);
    if ttl > 0 && status_raw.eq_ignore_ascii_case("NOERROR") {
        state
            .dns_cache
            .insert(cache_key, response_bytes.clone(), ttl)
            .await;
    }

    let _ = (identity_id, client_ip, server_name);
    Ok(DnsQueryResult {
        response_bytes,
        query_name,
        query_type,
        row_time,
        row_time_ms,
        status_raw,
        cached: false,
        upstream,
        elapsed_ms,
        filter_id: None,
        rule_text: String::new(),
        reason: "Forwarded".to_string(),
    })
}

async fn enqueue_live_query(
    state: &AppState,
    protocol: NativeProtocol,
    identity_id: &str,
    client_ip: &str,
    original_client: &str,
    server_name: Option<&str>,
    result: &DnsQueryResult,
) {
    let compiled_policy = state.compiled_policy();
    let row = build_live_write_row(
        LiveMirrorSeed {
            row_fingerprint: live_row_fingerprint(
                protocol,
                identity_id,
                client_ip,
                server_name,
                result,
            ),
            row_time: result.row_time.clone(),
            row_time_ms: result.row_time_ms,
            identity_id: identity_id.to_string(),
            client: client_ip.to_string(),
            original_client: original_client.to_string(),
            protocol: protocol.as_querylog_proto().to_string(),
            query_name: result.query_name.clone(),
            query_type: result.query_type.clone(),
            status_raw: result.status_raw.clone(),
            reason: result.reason.clone(),
            upstream: result.upstream.clone(),
            elapsed_ms: result.elapsed_ms,
            cached: result.cached,
            filter_id: result.filter_id,
            rule: result.rule_text.clone(),
            service_name: String::new(),
        },
        &compiled_policy.filter_lookup,
    );

    state
        .native_query_writer
        .send(NativeQueryEvent { row })
        .await;
}

async fn run_query_writer(config: RuntimeConfig, mut receiver: mpsc::Receiver<NativeQueryEvent>) {
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    let mut writer = match db.open_writer() {
        Ok(writer) => writer,
        Err(error) => {
            warn!("failed to bootstrap native query writer schema: {error:#}");
            return;
        }
    };

    let mut batch = Vec::new();
    loop {
        match tokio::time::timeout(DNS_QUERY_WRITER_FLUSH_INTERVAL, receiver.recv()).await {
            Ok(Some(event)) => {
                batch.push(event);
                if batch.len() >= DNS_QUERY_WRITER_BATCH_SIZE {
                    flush_query_batch(&mut writer, &batch).await;
                    batch.clear();
                }
            }
            Ok(None) => {
                if !batch.is_empty() {
                    flush_query_batch(&mut writer, &batch).await;
                }
                return;
            }
            Err(_) => {
                if !batch.is_empty() {
                    flush_query_batch(&mut writer, &batch).await;
                    batch.clear();
                }
            }
        }
    }
}

async fn run_querylog_search_queue_worker(config: RuntimeConfig) {
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    if let Err(error) = db.ensure_schema() {
        warn!("failed to bootstrap querylog search queue worker: {error:#}");
        return;
    }

    let mut interval = tokio::time::interval(DNS_QUERY_SEARCH_INDEX_INTERVAL);
    loop {
        interval.tick().await;
        if let Err(error) = db.process_querylog_search_queue(DNS_QUERY_SEARCH_INDEX_BATCH_SIZE) {
            warn!("failed to advance querylog search queue: {error:#}");
        }
    }
}

async fn run_querylog_archive_worker(config: RuntimeConfig) {
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    if let Err(error) = db.ensure_schema() {
        warn!("failed to bootstrap querylog archive worker: {error:#}");
        return;
    }

    let mut interval = tokio::time::interval(DNS_QUERY_ARCHIVE_INTERVAL);
    loop {
        interval.tick().await;
        if let Err(error) = db.archive_querylog_history(7, DNS_QUERY_ARCHIVE_MAX_SHARDS) {
            warn!("failed to archive old querylog history: {error:#}");
        }
    }
}

async fn flush_query_batch(writer: &mut ControlPlaneDbWriter, batch: &[NativeQueryEvent]) {
    if batch.is_empty() {
        return;
    }
    let rows = batch
        .iter()
        .map(|event| event.row.clone())
        .collect::<Vec<_>>();
    if let Err(error) = writer.insert_querylog_rows(&rows) {
        warn!("failed to write live querylog rows: {error:#}");
    }
}

pub(crate) fn parse_source_config_text(raw: &str, source_label: &str) -> Result<SourceConfig> {
    serde_yaml::from_str(raw).with_context(|| format!("parse {source_label}"))
}

pub(crate) fn source_config_yaml(source: &SourceConfig) -> Result<String> {
    serde_yaml::to_string(source).context("serialize native source config")
}

pub(crate) async fn compile_source_config(
    source: SourceConfig,
    client: &reqwest::Client,
) -> Result<CompiledPolicy> {
    if source.upstreams.is_empty() {
        bail!("source config must include at least one upstream");
    }
    let compiled_at = now_iso8601();
    let mut policy = CompiledPolicy {
        schema_version: source.schema_version.unwrap_or(1),
        compiled_at,
        source_hash: String::new(),
        compiled_hash: String::new(),
        upstreams: source
            .upstreams
            .into_iter()
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty())
            .collect(),
        filter_lookup: BTreeMap::new(),
        allow_exact: BTreeMap::new(),
        allow_suffix: BTreeMap::new(),
        block_exact: BTreeMap::new(),
        block_suffix: BTreeMap::new(),
    };
    if policy.upstreams.is_empty() {
        bail!("source config upstream list is empty after normalization");
    }

    for entry in source.filters {
        if !filter_enabled(&entry.enabled) {
            continue;
        }
        let filter_id = entry
            .id
            .or_else(|| Some(stable_filter_id(&entry.name, &entry.url)));
        let lines = load_filter_lines(client, &entry.url).await?;
        policy
            .filter_lookup
            .insert(filter_id.unwrap_or_default(), entry.name.clone());
        for line in lines {
            if let Some(parsed) = parse_rule_line(&line, false, filter_id, &entry.name)
                .context("parse filter rule")?
            {
                insert_rule(&mut policy, parsed, false);
            }
        }
    }

    for entry in source.whitelist_filters {
        if !filter_enabled(&entry.enabled) {
            continue;
        }
        let filter_id = entry
            .id
            .or_else(|| Some(stable_filter_id(&entry.name, &entry.url)));
        let lines = load_filter_lines(client, &entry.url).await?;
        policy
            .filter_lookup
            .insert(filter_id.unwrap_or_default(), entry.name.clone());
        for line in lines {
            if let Some(parsed) = parse_rule_line(&line, true, filter_id, &entry.name)
                .context("parse whitelist rule")?
            {
                insert_rule(&mut policy, parsed, true);
            }
        }
    }

    for raw_rule in source.user_rules {
        let Some(parsed) = parse_rule_line(&raw_rule, false, None, "user_rules")? else {
            continue;
        };
        let allow = parsed.allow;
        insert_rule(&mut policy, parsed, allow);
    }

    Ok(policy)
}

pub(crate) async fn prepare_compiled_policy_write(
    source: &SourceConfig,
    client: &reqwest::Client,
) -> Result<PreparedCompiledPolicyWrite> {
    let raw = source_config_yaml(source)?;
    let source_hash = hex::encode(Sha256::digest(raw.as_bytes()));
    let mut compiled = compile_source_config(source.clone(), client).await?;
    compiled.source_hash = source_hash;
    let compiled_bytes = serde_json::to_vec_pretty(&compiled)?;
    compiled.compiled_hash = hex::encode(Sha256::digest(&compiled_bytes));
    let final_bytes = serde_json::to_vec_pretty(&compiled)?;
    Ok(PreparedCompiledPolicyWrite {
        source_bytes: format!("{raw}\n").into_bytes(),
        compiled,
        compiled_bytes: final_bytes,
    })
}

pub(crate) fn persist_prepared_compiled_policy(
    config: &RuntimeConfig,
    prepared: &PreparedCompiledPolicyWrite,
) -> Result<()> {
    write_atomic(&config.source_config_file, &prepared.source_bytes)?;
    write_atomic(&config.compiled_policy_file, &prepared.compiled_bytes)?;
    Ok(())
}

async fn load_filter_lines(client: &reqwest::Client, location: &str) -> Result<Vec<String>> {
    if location.starts_with("http://") || location.starts_with("https://") {
        let body = client
            .get(location)
            .send()
            .await
            .with_context(|| format!("fetch filter list {location}"))?
            .error_for_status()
            .with_context(|| format!("download filter list {location}"))?
            .text()
            .await
            .with_context(|| format!("read filter list body {location}"))?;
        return Ok(body.lines().map(|line| line.to_string()).collect());
    }
    let path = if let Some(stripped) = location.strip_prefix("file://") {
        stripped
    } else {
        location
    };
    let file = fs::File::open(path).with_context(|| format!("open filter list {path}"))?;
    Ok(BufReader::new(file)
        .lines()
        .collect::<std::result::Result<Vec<_>, io::Error>>()
        .with_context(|| format!("read filter list {path}"))?)
}

fn insert_rule(policy: &mut CompiledPolicy, parsed: ParsedRule, force_allow: bool) {
    let allow = force_allow || parsed.allow;
    let target = if allow {
        if parsed.suffix {
            &mut policy.allow_suffix
        } else {
            &mut policy.allow_exact
        }
    } else if parsed.suffix {
        &mut policy.block_suffix
    } else {
        &mut policy.block_exact
    };
    target.entry(parsed.domain).or_insert(parsed.source);
}

#[derive(Debug)]
struct ParsedRule {
    domain: String,
    suffix: bool,
    allow: bool,
    source: CompiledRuleSource,
}

fn parse_rule_line(
    raw_line: &str,
    treat_as_allow: bool,
    filter_id: Option<i64>,
    label: &str,
) -> Result<Option<ParsedRule>> {
    let trimmed = raw_line.trim();
    if trimmed.is_empty()
        || trimmed.starts_with('!')
        || trimmed.starts_with('#')
        || trimmed.starts_with('[')
    {
        return Ok(None);
    }

    if let Some(domain) = parse_hosts_style_rule(trimmed) {
        return Ok(Some(ParsedRule {
            domain,
            suffix: false,
            allow: treat_as_allow,
            source: CompiledRuleSource {
                filter_id,
                label: label.to_string(),
                rule_text: trimmed.to_string(),
            },
        }));
    }

    let (allow, body) = if let Some(stripped) = trimmed.strip_prefix("@@") {
        (true, stripped)
    } else {
        (treat_as_allow, trimmed)
    };

    if let Some(domain) = parse_adblock_domain(body) {
        return Ok(Some(ParsedRule {
            domain,
            suffix: body.starts_with("||"),
            allow,
            source: CompiledRuleSource {
                filter_id,
                label: label.to_string(),
                rule_text: trimmed.to_string(),
            },
        }));
    }

    if is_domain_like(trimmed) {
        return Ok(Some(ParsedRule {
            domain: normalize_domain(trimmed),
            suffix: false,
            allow,
            source: CompiledRuleSource {
                filter_id,
                label: label.to_string(),
                rule_text: trimmed.to_string(),
            },
        }));
    }

    if label == "user_rules" {
        bail!("unsupported user rule syntax: {trimmed}");
    }

    Ok(None)
}

fn parse_hosts_style_rule(value: &str) -> Option<String> {
    let mut parts = value.split_whitespace();
    let first = parts.next()?;
    let host = parts.next()?;
    if parts.next().is_some() {
        return None;
    }
    if !matches!(first, "0.0.0.0" | "127.0.0.1" | "::" | "::1") {
        return None;
    }
    let host = normalize_domain(host);
    if host.is_empty() || !is_domain_like(&host) {
        return None;
    }
    Some(host)
}

fn parse_adblock_domain(value: &str) -> Option<String> {
    let raw = value.trim();
    let raw = raw.strip_prefix("||").unwrap_or(raw);
    let raw = raw.split('$').next().unwrap_or(raw);
    let raw = raw.trim_end_matches('^').trim_end_matches('|');
    let raw = raw.split('/').next().unwrap_or(raw);
    let normalized = normalize_domain(raw);
    if normalized.is_empty() || !is_domain_like(&normalized) {
        return None;
    }
    Some(normalized)
}

fn filter_enabled(value: &serde_yaml::Value) -> bool {
    match value {
        serde_yaml::Value::Bool(flag) => *flag,
        serde_yaml::Value::Number(number) => number.as_i64().unwrap_or_default() != 0,
        serde_yaml::Value::String(text) => {
            matches!(
                text.trim().to_ascii_lowercase().as_str(),
                "1" | "true" | "yes" | "on"
            )
        }
        _ => false,
    }
}

fn normalize_domain(value: &str) -> String {
    value.trim().trim_matches('.').to_ascii_lowercase()
}

fn is_domain_like(value: &str) -> bool {
    let normalized = normalize_domain(value);
    if normalized.is_empty() || !normalized.contains('.') {
        return false;
    }
    normalized
        .chars()
        .all(|ch| ch.is_ascii_alphanumeric() || matches!(ch, '.' | '-' | '_'))
}

fn match_suffix_rule(
    rules: &BTreeMap<String, CompiledRuleSource>,
    query_name: &str,
) -> Option<CompiledRuleSource> {
    let labels = query_name.split('.').collect::<Vec<_>>();
    for index in 0..labels.len() {
        let candidate = labels[index..].join(".");
        if let Some(source) = rules.get(&candidate) {
            return Some(source.clone());
        }
    }
    None
}

fn cache_key_for_query(query_name: &str, query_type: &str) -> String {
    format!(
        "{}|{}",
        normalize_domain(query_name),
        query_type.to_ascii_uppercase()
    )
}

fn stable_filter_id(name: &str, url: &str) -> i64 {
    let mut digest = Sha256::new();
    digest.update(name.as_bytes());
    digest.update(b"\n");
    digest.update(url.as_bytes());
    let bytes = digest.finalize();
    i64::from_be_bytes([
        bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7],
    ])
    .abs()
}

fn live_row_fingerprint(
    protocol: NativeProtocol,
    identity_id: &str,
    client_ip: &str,
    server_name: Option<&str>,
    result: &DnsQueryResult,
) -> String {
    let mut digest = Sha256::new();
    digest.update(protocol.as_querylog_proto().as_bytes());
    digest.update(identity_id.as_bytes());
    digest.update(client_ip.as_bytes());
    digest.update(server_name.unwrap_or_default().as_bytes());
    digest.update(result.row_time.as_bytes());
    digest.update(result.query_name.as_bytes());
    digest.update(result.query_type.as_bytes());
    digest.update(result.status_raw.as_bytes());
    digest.update(&result.response_bytes);
    hex::encode(digest.finalize())
}

async fn forward_query_to_doh(
    client: &reqwest::Client,
    upstream: &str,
    query_bytes: &[u8],
) -> Result<Vec<u8>> {
    let response = client
        .post(upstream)
        .header(CONTENT_TYPE, "application/dns-message")
        .header(ACCEPT, "application/dns-message")
        .body(query_bytes.to_vec())
        .send()
        .await
        .with_context(|| format!("forward DNS query to upstream {upstream}"))?
        .error_for_status()
        .with_context(|| format!("upstream responded with error for {upstream}"))?;
    Ok(response
        .bytes()
        .await
        .with_context(|| format!("read upstream response body from {upstream}"))?
        .to_vec())
}

fn minimum_ttl(message: &Message) -> Option<u32> {
    message
        .answers()
        .iter()
        .map(Record::ttl)
        .min()
        .or_else(|| message.name_servers().iter().map(Record::ttl).min())
}

fn build_blocked_response(
    request: &Message,
    query_name: &str,
    record_type: RecordType,
) -> Result<Vec<u8>> {
    let mut response = Message::new();
    response
        .set_id(request.id())
        .set_message_type(hickory_proto::op::MessageType::Response)
        .set_op_code(request.op_code())
        .set_recursion_desired(request.recursion_desired())
        .set_recursion_available(true)
        .set_response_code(ResponseCode::NXDomain);
    if let Some(query) = request.query().cloned() {
        response.add_query(query);
    }

    match record_type {
        RecordType::A => {
            let name = hickory_proto::rr::Name::from_ascii(query_name)
                .with_context(|| format!("build blocked A response for {query_name}"))?;
            let record =
                Record::from_rdata(name, BLOCKED_TTL_SECONDS, RData::A(A::new(0, 0, 0, 0)));
            response.add_answer(record);
            response.set_response_code(ResponseCode::NoError);
        }
        RecordType::AAAA => {
            let name = hickory_proto::rr::Name::from_ascii(query_name)
                .with_context(|| format!("build blocked AAAA response for {query_name}"))?;
            let record = Record::from_rdata(
                name,
                BLOCKED_TTL_SECONDS,
                RData::AAAA(AAAA::new(0, 0, 0, 0, 0, 0, 0, 0)),
            );
            response.add_answer(record);
            response.set_response_code(ResponseCode::NoError);
        }
        _ => {}
    }

    response.to_bytes().context("encode blocked dns response")
}

fn extract_dns_query_param(query: &str) -> Option<String> {
    url::form_urlencoded::parse(query.as_bytes())
        .find(|(key, _)| key == "dns")
        .map(|(_, value)| value.into_owned())
}

fn extract_client_ip(headers: &HeaderMap, remote_addr: Option<SocketAddr>) -> Option<IpAddr> {
    for header_name in ["x-forwarded-for", "cf-connecting-ip", "x-real-ip"] {
        if let Some(ip) = headers
            .get(header_name)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.split(',').next())
            .map(str::trim)
            .and_then(|value| value.parse::<IpAddr>().ok())
        {
            return Some(ip);
        }
    }
    remote_addr.map(|addr| addr.ip())
}

fn resolve_doh_identity(
    token: &Option<String>,
    metadata: &crate::querylog_sync::MetadataLookup,
) -> String {
    match token {
        None => "__bare__".to_string(),
        Some(token) => metadata
            .by_token()
            .get(token)
            .cloned()
            .unwrap_or_else(|| "__unknown__".to_string()),
    }
}

fn resolve_dot_identity(
    server_name: Option<&str>,
    metadata: &crate::querylog_sync::MetadataLookup,
) -> String {
    let Some(server_name) = server_name
        .map(normalize_domain)
        .filter(|value| !value.is_empty())
    else {
        return "__bare__".to_string();
    };
    let label = server_name.split('.').next().unwrap_or_default();
    metadata
        .by_dot_label()
        .get(label)
        .cloned()
        .unwrap_or_else(|| "__bare__".to_string())
}

fn dot_server_name(stream: &tokio_rustls::server::TlsStream<TcpStream>) -> Option<String> {
    stream
        .get_ref()
        .1
        .server_name()
        .map(|value| value.to_string())
}

fn tls_acceptor(config: &RuntimeConfig) -> Result<TlsAcceptor> {
    Ok(TlsAcceptor::from(Arc::new(load_tls_server_config(config)?)))
}

pub(crate) fn load_tls_server_config(config: &RuntimeConfig) -> Result<rustls::ServerConfig> {
    let certs = load_certificates(&config.tls_cert_file)?;
    let key = load_private_key(&config.tls_key_file)?;
    let mut tls = rustls::ServerConfig::builder()
        .with_no_client_auth()
        .with_single_cert(certs, key)
        .context("build rustls server config")?;
    tls.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec(), b"dns".to_vec()];
    Ok(tls)
}

fn load_certificates(path: &Path) -> Result<Vec<CertificateDer<'static>>> {
    let raw = fs::read(path).with_context(|| format!("read {}", path.display()))?;
    let mut reader = BufReader::new(Cursor::new(raw));
    rustls_pemfile::certs(&mut reader)
        .collect::<std::result::Result<Vec<_>, _>>()
        .context("parse certificate pem")
}

fn load_private_key(path: &Path) -> Result<PrivateKeyDer<'static>> {
    let raw = fs::read(path).with_context(|| format!("read {}", path.display()))?;
    let mut reader = BufReader::new(Cursor::new(raw));
    rustls_pemfile::private_key(&mut reader)
        .context("parse private key pem")?
        .ok_or_else(|| anyhow!("missing private key in {}", path.display()))
}
