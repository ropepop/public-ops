mod native_runtime;
mod public_surface;
mod querylog_sync;
mod whois_cache;

use std::collections::BTreeMap;
use std::fs;
use std::net::{IpAddr, SocketAddr};
use std::path::Path;
use std::sync::{
    atomic::{AtomicBool, Ordering},
    Arc, RwLock,
};
use std::time::Duration;

use anyhow::{Context, Result};
use arbuzas_dns_lib::config::{load_runtime_env_defaults, RuntimeConfig};
use arbuzas_dns_lib::state::{
    now_iso8601, AdminRuntimeSettings, ControlPlaneDb, LegacyObservabilityCleanupReport,
    QuerylogStorageInspection,
};
use axum::extract::State;
use axum::http::StatusCode;
use axum::response::IntoResponse;
use axum::routing::{any, get, post};
use axum::{Json, Router};
use axum_server::tls_rustls::RustlsConfig;
use clap::{Args, Parser, Subcommand};
use serde_json::{json, Value};
use tokio::signal;
use tokio::sync::Mutex;
use tracing::info;

#[derive(Parser, Debug)]
#[command(name = "arbuzas-dns")]
#[command(about = "Arbuzas DNS control-plane and runtime helper")]
struct Cli {
    #[command(subcommand)]
    command: TopLevelCommand,
}

#[derive(Subcommand, Debug)]
enum TopLevelCommand {
    Serve(ServeArgs),
    Migrate(MigrateArgs),
    Health(HealthArgs),
    Compact(CompactArgs),
    Release(ReleaseArgs),
}

#[derive(Args, Debug, Clone)]
struct ServeArgs {}

#[derive(Args, Debug, Clone)]
struct MigrateArgs {
    #[arg(long)]
    json: bool,
}

#[derive(Args, Debug, Clone)]
struct HealthArgs {
    #[arg(long)]
    json: bool,
    #[arg(long)]
    strict: bool,
}

#[derive(Args, Debug, Clone)]
struct CompactArgs {
    #[arg(long)]
    json: bool,
    #[arg(long)]
    include_legacy_observability: bool,
}

#[derive(Args, Debug, Clone)]
struct ReleaseArgs {
    #[command(subcommand)]
    command: ReleaseCommand,
}

#[derive(Subcommand, Debug, Clone)]
enum ReleaseCommand {
    SyncPolicy {
        #[arg(long)]
        json: bool,
    },
    Validate {
        #[arg(long)]
        json: bool,
    },
}

#[derive(Clone)]
struct AppState {
    config: RuntimeConfig,
    db: ControlPlaneDb,
    client: reqwest::Client,
    compiled_policy: Arc<RwLock<Arc<native_runtime::CompiledPolicy>>>,
    runtime_settings: Arc<RwLock<AdminRuntimeSettings>>,
    dns_cache: native_runtime::DnsResponseCache,
    native_query_writer: native_runtime::NativeQueryWriterControl,
    runtime_write_gate: Arc<Mutex<()>>,
    runtime_ready: Arc<AtomicBool>,
    sync_trigger: querylog_sync::SyncTriggerControl,
}

impl AppState {
    async fn write_serialized<T, F>(&self, op: F) -> Result<T>
    where
        F: FnOnce(&ControlPlaneDb) -> Result<T>,
    {
        let _guard = self.runtime_write_gate.lock().await;
        op(&self.db)
    }

    fn compiled_policy(&self) -> Arc<native_runtime::CompiledPolicy> {
        self.compiled_policy
            .read()
            .expect("compiled policy lock poisoned")
            .clone()
    }

    fn replace_compiled_policy(&self, policy: Arc<native_runtime::CompiledPolicy>) {
        *self
            .compiled_policy
            .write()
            .expect("compiled policy lock poisoned") = policy;
    }

    fn runtime_settings(&self) -> AdminRuntimeSettings {
        self.runtime_settings
            .read()
            .expect("runtime settings lock poisoned")
            .clone()
    }

    fn replace_runtime_settings(&self, settings: AdminRuntimeSettings) {
        *self
            .runtime_settings
            .write()
            .expect("runtime settings lock poisoned") = settings;
    }

}

#[derive(Debug, Clone)]
struct ValidationOutcome {
    payload: Value,
    errors: Vec<String>,
}

#[tokio::main]
async fn main() -> Result<()> {
    let _ = tokio_rustls::rustls::crypto::aws_lc_rs::default_provider().install_default();
    tracing_subscriber::fmt()
        .with_env_filter(
            std::env::var("RUST_LOG")
                .unwrap_or_else(|_| "arbuzas_dns=info,arbuzas_dns_lib=info".to_string()),
        )
        .compact()
        .init();

    load_runtime_env_defaults();
    let cli = Cli::parse();
    match cli.command {
        TopLevelCommand::Serve(args) => run_serve(args).await,
        TopLevelCommand::Migrate(args) => run_migrate(args),
        TopLevelCommand::Health(args) => run_health(args),
        TopLevelCommand::Compact(args) => run_compact(args),
        TopLevelCommand::Release(args) => run_release(args).await,
    }
}

async fn run_serve(_: ServeArgs) -> Result<()> {
    let config = RuntimeConfig::from_env();
    config.ensure_runtime_dirs()?;
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    db.bootstrap_runtime_state(&config)?;
    let sync_trigger = querylog_sync::sync_trigger_control(&config);

    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(15))
        .build()
        .context("build HTTP client")?;
    if !config.compiled_policy_file.is_file() {
        info!(
            "compiled policy missing at {}; compiling from source config",
            config.compiled_policy_file.display()
        );
        native_runtime::prepare_compiled_policy(&config, &client).await?;
    }
    let compiled_policy = native_runtime::load_compiled_policy(&config)?;
    let runtime_settings = db.admin_runtime_settings(&config)?;
    sync_trigger
        .remember_filter_lookup(&compiled_policy.filter_lookup)
        .await;
    let (native_query_writer, native_query_writer_tasks) =
        native_runtime::start_query_writer(&config);

    let state = AppState {
        config: config.clone(),
        db,
        client,
        compiled_policy: Arc::new(RwLock::new(compiled_policy)),
        runtime_settings: Arc::new(RwLock::new(runtime_settings)),
        dns_cache: native_runtime::DnsResponseCache::default(),
        native_query_writer,
        runtime_write_gate: Arc::new(Mutex::new(())),
        runtime_ready: Arc::new(AtomicBool::new(false)),
        sync_trigger: sync_trigger.clone(),
    };
    let dns_tasks = native_runtime::start_dns_listeners(state.clone())?;
    state.runtime_ready.store(true, Ordering::SeqCst);

    let internal_router = Router::new()
        .route("/", any(public_surface::handle_public_request))
        .route("/index.html", any(public_surface::handle_public_request))
        .route("/login", any(public_surface::handle_public_request))
        .route("/login.html", any(public_surface::handle_public_request))
        .route("/v1/health", get(api_health))
        .route("/v1/compact", post(api_compact))
        .route("/v1/release/validate", post(api_release_validate))
        .route("/livez", get(api_livez))
        .route("/healthz", get(api_health))
        .route("/{*path}", any(public_surface::handle_public_request))
        .with_state(state.clone());
    let https_router = Router::new()
        .route(
            "/",
            any(public_surface::handle_public_request_with_connect_info),
        )
        .route(
            "/index.html",
            any(public_surface::handle_public_request_with_connect_info),
        )
        .route(
            "/login",
            any(public_surface::handle_public_request_with_connect_info),
        )
        .route(
            "/login.html",
            any(public_surface::handle_public_request_with_connect_info),
        )
        .route(
            "/{*path}",
            any(public_surface::handle_public_request_with_connect_info),
        )
        .with_state(state.clone());

    if config.controlplane_socket.exists() {
        fs::remove_file(&config.controlplane_socket).with_context(|| {
            format!(
                "remove stale socket {}",
                config.controlplane_socket.display()
            )
        })?;
    }

    let tcp_listener = tokio::net::TcpListener::bind((
        config.controlplane_host.as_str(),
        config.controlplane_port,
    ))
    .await
    .with_context(|| {
        format!(
            "bind controlplane TCP listener {}:{}",
            config.controlplane_host, config.controlplane_port
        )
    })?;
    let unix_listener =
        tokio::net::UnixListener::bind(&config.controlplane_socket).with_context(|| {
            format!(
                "bind controlplane socket {}",
                config.controlplane_socket.display()
            )
        })?;

    let https_config = RustlsConfig::from_pem_file(&config.tls_cert_file, &config.tls_key_file)
        .await
        .context("load HTTPS TLS configuration")?;
    let https_addr = SocketAddr::new(
        config
            .dns_bind_host
            .parse::<IpAddr>()
            .unwrap_or(IpAddr::from([0, 0, 0, 0])),
        config.https_port,
    );

    info!(
        "native controlplane listening on tcp {}:{} and {}",
        config.controlplane_host,
        config.controlplane_port,
        config.controlplane_socket.display()
    );
    info!("native HTTPS/DoH/UI listening on {}", https_addr);

    let tcp_router = internal_router.clone();
    let unix_router = internal_router;
    let https_service = https_router;
    let tcp_task = tokio::spawn(async move { axum::serve(tcp_listener, tcp_router).await });
    let unix_task = tokio::spawn(async move { axum::serve(unix_listener, unix_router).await });
    let https_task = tokio::spawn(async move {
        axum_server::bind_rustls(https_addr, https_config)
            .serve(https_service.into_make_service_with_connect_info::<SocketAddr>())
            .await
    });

    signal::ctrl_c().await.context("wait for shutdown signal")?;
    info!("shutting down native arbuzas dns runtime");
    tcp_task.abort();
    unix_task.abort();
    https_task.abort();
    for task in native_query_writer_tasks {
        task.abort();
    }
    for task in dns_tasks {
        task.abort();
    }
    Ok(())
}

fn run_migrate(args: MigrateArgs) -> Result<()> {
    let config = RuntimeConfig::from_env();
    config.ensure_runtime_dirs()?;
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    db.ensure_schema()?;

    let mut summary = BTreeMap::new();
    summary.insert(
        "runtimeFiles".to_string(),
        db.import_legacy_runtime_files(&config)?,
    );
    summary.insert(
        "observability".to_string(),
        db.import_legacy_observability_once(&config.legacy_observability_db_file)?,
    );
    summary.insert(
        "querylogCanonicalization".to_string(),
        db.canonicalize_querylog_payloads()?,
    );
    summary.insert("compact".to_string(), db.compact()?);
    let payload = json!({
        "status": "ok",
        "dbPath": config.controlplane_db.display().to_string(),
        "imports": summary,
    });
    if args.json {
        print_json(&payload);
    } else {
        println!("{}", serde_json::to_string_pretty(&payload)?);
    }
    Ok(())
}

fn run_health(args: HealthArgs) -> Result<()> {
    let config = RuntimeConfig::from_env();
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    let payload = db.health_payload(&config)?;
    if args.strict {
        for path in [
            config.controlplane_db.as_path(),
            config.source_config_file.as_path(),
            config.compiled_policy_file.as_path(),
            config.tls_cert_file.as_path(),
            config.tls_key_file.as_path(),
        ] {
            if !path.exists() {
                anyhow::bail!("required runtime artifact missing: {}", path.display());
            }
        }
    }
    if args.json {
        print_json(&payload);
    } else {
        println!("{}", serde_json::to_string_pretty(&payload)?);
    }
    Ok(())
}

fn run_compact(args: CompactArgs) -> Result<()> {
    let config = RuntimeConfig::from_env();
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    let controlplane = db.compact()?;
    let legacy = if args.include_legacy_observability {
        Some(db.cleanup_legacy_observability_file(&config.legacy_observability_db_file)?)
    } else {
        None
    };
    let legacy_removed = legacy
        .as_ref()
        .map(|report| report.removed)
        .unwrap_or(false);
    let payload = json!({
        "controlplane": controlplane,
        "legacyObservability": legacy.as_ref().map(legacy_cleanup_report_json),
        "legacyObservabilityRemoved": legacy_removed,
    });
    if args.json {
        print_json(&payload);
    } else {
        println!("{}", serde_json::to_string_pretty(&payload)?);
    }
    Ok(())
}

async fn run_release(args: ReleaseArgs) -> Result<()> {
    let config = RuntimeConfig::from_env();
    let db = ControlPlaneDb::new(config.controlplane_db.clone());
    match args.command {
        ReleaseCommand::SyncPolicy { json } => {
            let payload = sync_policy_state(&config).await?;
            if json {
                print_json(&payload);
            } else {
                println!("{}", serde_json::to_string_pretty(&payload)?);
            }
        }
        ReleaseCommand::Validate { json } => {
            let outcome = release_validate(&config, &db)?;
            if json {
                print_json(&outcome.payload);
            } else {
                println!("{}", serde_json::to_string_pretty(&outcome.payload)?);
            }
            if !outcome.errors.is_empty() {
                anyhow::bail!("release validation failed: {}", outcome.errors.join("; "));
            }
        }
    }
    Ok(())
}

async fn api_health(State(state): State<AppState>) -> impl IntoResponse {
    match state.db.health_payload(&state.config) {
        Ok(payload) => (StatusCode::OK, Json(payload)).into_response(),
        Err(error) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({ "error": error.to_string() })),
        )
            .into_response(),
    }
}

async fn api_livez(State(state): State<AppState>) -> impl IntoResponse {
    if state.runtime_ready.load(Ordering::SeqCst) {
        (StatusCode::OK, Json(json!({ "status": "ok" }))).into_response()
    } else {
        (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({ "status": "starting" })),
        )
            .into_response()
    }
}

async fn api_compact(State(state): State<AppState>) -> impl IntoResponse {
    match state.db.compact() {
        Ok(payload) => (StatusCode::OK, Json(payload)).into_response(),
        Err(error) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({ "error": error.to_string() })),
        )
            .into_response(),
    }
}

async fn api_release_validate(State(state): State<AppState>) -> impl IntoResponse {
    match release_validate(&state.config, &state.db) {
        Ok(outcome) => {
            let status = if outcome.errors.is_empty() {
                StatusCode::OK
            } else {
                StatusCode::SERVICE_UNAVAILABLE
            };
            (status, Json(outcome.payload)).into_response()
        }
        Err(error) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({ "error": error.to_string() })),
        )
            .into_response(),
    }
}

fn print_json(payload: &Value) {
    println!(
        "{}",
        serde_json::to_string_pretty(payload).unwrap_or_else(|_| "{}".to_string())
    );
}

fn storage_inspection_json(inspection: &QuerylogStorageInspection) -> Value {
    json!({
        "autoVacuum": inspection.auto_vacuum,
        "vacuumMode": inspection.vacuum_mode,
        "pageSize": inspection.page_size,
        "pageCount": inspection.page_count,
        "freelistCount": inspection.freelist_count,
        "freeBytes": inspection.free_bytes,
        "freeRatio": inspection.free_ratio,
        "quickCheck": inspection.quick_check.clone().unwrap_or_default(),
        "missingDetails": inspection.missing_details,
        "orphanedDetails": inspection.orphaned_details,
        "legacyTransitionCompleted": inspection.legacy_transition_completed,
        "legacyRowsRemaining": inspection.legacy_rows_remaining,
        "legacyTableRowCounts": inspection.legacy_table_row_counts,
    })
}

fn legacy_cleanup_report_json(report: &LegacyObservabilityCleanupReport) -> Value {
    json!({
        "status": report.status,
        "dbPath": report.db_path,
        "removed": report.removed,
        "gate": {
            "eligible": report.gate.eligible,
            "transitionCompleted": report.gate.transition_completed,
            "legacyRowsRemaining": report.gate.legacy_rows_remaining,
            "legacyTableRowCounts": report.gate.legacy_table_row_counts,
            "missingDetails": report.gate.missing_details,
            "orphanedDetails": report.gate.orphaned_details,
            "lastError": report.gate.last_error,
            "reasons": report.gate.reasons,
        }
    })
}

async fn sync_policy_state(config: &RuntimeConfig) -> Result<Value> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(30))
        .build()
        .context("build native policy compiler client")?;
    native_runtime::prepare_compiled_policy(config, &client).await
}

fn exists_check(path: &Path) -> Value {
    json!({
        "exists": path.is_file(),
        "path": path.display().to_string(),
    })
}

fn valid_check(path: &Path, valid: bool) -> Value {
    json!({
        "exists": path.is_file(),
        "valid": valid,
        "path": path.display().to_string(),
    })
}

fn release_validate(config: &RuntimeConfig, db: &ControlPlaneDb) -> Result<ValidationOutcome> {
    let mut checks = BTreeMap::new();
    checks.insert(
        "controlplaneDb".to_string(),
        exists_check(&config.controlplane_db),
    );
    let source_yaml_ok = fs::read_to_string(&config.source_config_file)
        .ok()
        .and_then(|raw| serde_yaml::from_str::<serde_yaml::Value>(&raw).ok())
        .is_some();
    checks.insert(
        "sourceConfigYaml".to_string(),
        valid_check(&config.source_config_file, source_yaml_ok),
    );
    let compiled_policy_ok = fs::read(&config.compiled_policy_file)
        .ok()
        .and_then(|raw| serde_json::from_slice::<Value>(&raw).ok())
        .is_some();
    checks.insert(
        "compiledPolicy".to_string(),
        valid_check(&config.compiled_policy_file, compiled_policy_ok),
    );
    checks.insert("tlsCert".to_string(), exists_check(&config.tls_cert_file));
    checks.insert("tlsKey".to_string(), exists_check(&config.tls_key_file));
    checks.insert(
        "legacyObservabilityDb".to_string(),
        exists_check(&config.legacy_observability_db_file),
    );
    let storage = db.inspect_querylog_storage(true)?;
    checks.insert("dbStorage".to_string(), storage_inspection_json(&storage));
    checks.insert("dbHealth".to_string(), db.health_payload(config)?);
    let mut errors = Vec::new();
    if !config.controlplane_db.is_file() {
        errors.push(format!(
            "control-plane database missing: {}",
            config.controlplane_db.display()
        ));
    }
    if !source_yaml_ok {
        errors.push(format!(
            "native source config is missing or invalid YAML: {}",
            config.source_config_file.display()
        ));
    }
    if !compiled_policy_ok {
        errors.push(format!(
            "compiled policy snapshot is missing or invalid JSON: {}",
            config.compiled_policy_file.display()
        ));
    }
    for (label, path) in [
        ("TLS certificate", config.tls_cert_file.as_path()),
        ("TLS private key", config.tls_key_file.as_path()),
    ] {
        if !path.is_file() {
            errors.push(format!("{label} missing: {}", path.display()));
        }
    }
    if storage.legacy_transition_completed && storage.auto_vacuum != 2 {
        errors.push(format!(
            "controlplane database {} is still in auto_vacuum mode {} ({}) after legacy transition",
            config.controlplane_db.display(),
            storage.auto_vacuum,
            storage.vacuum_mode
        ));
    }
    let payload = json!({
        "status": if errors.is_empty() { "ok" } else { "error" },
        "validatedAt": now_iso8601(),
        "checks": checks,
        "errors": errors.clone(),
    });
    Ok(ValidationOutcome { payload, errors })
}
