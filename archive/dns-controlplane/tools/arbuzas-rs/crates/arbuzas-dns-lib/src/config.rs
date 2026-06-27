use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result};

pub const QUERYLOG_DEFAULT_VIEW_IMPROVED: &str = "improved";
pub const QUERYLOG_DEFAULT_VIEW_NATIVE: &str = "native";

#[derive(Debug, Clone)]
pub struct RuntimeConfig {
    pub dns_dir: PathBuf,
    pub state_dir: PathBuf,
    pub runtime_dir: PathBuf,
    pub log_dir: PathBuf,
    pub run_dir: PathBuf,
    pub controlplane_socket: PathBuf,
    pub controlplane_db: PathBuf,
    pub shadow_identities_file: PathBuf,
    pub shadow_querylog_view_preference_file: PathBuf,
    pub legacy_observability_db_file: PathBuf,
    pub adguard_querylog_dir: PathBuf,
    pub publisher_state_file: PathBuf,
    pub publisher_drift_file: PathBuf,
    pub publisher_health_file: PathBuf,
    pub source_config_file: PathBuf,
    pub compiled_policy_file: PathBuf,
    pub rendered_config_file: PathBuf,
    pub nginx_template_file: PathBuf,
    pub nginx_conf_file: PathBuf,
    pub nginx_state_file: PathBuf,
    pub runtime_env_file: PathBuf,
    pub tls_cert_file: PathBuf,
    pub tls_key_file: PathBuf,
    pub hostname: String,
    pub dot_hostname: String,
    pub dns_bind_host: String,
    pub dns_port: u16,
    pub https_port: u16,
    pub dot_port: u16,
    pub adguard_web_host: String,
    pub adguard_web_port: u16,
    pub admin_username: String,
    pub admin_password_file: PathBuf,
    pub admin_session_ttl_seconds: u64,
    pub adguard_admin_username: String,
    pub adguard_admin_password_file: PathBuf,
    pub controlplane_host: String,
    pub controlplane_port: u16,
    pub dot_backend_host: String,
    pub dot_backend_port: u16,
    pub dot_enabled: bool,
    pub dot_identity_enabled: bool,
    pub router_attr_enabled: bool,
    pub router_lan_ip: Option<String>,
    pub ddns_last_ipv4_file: PathBuf,
    pub nginx_bin: Option<String>,
    pub nginx_prefix: String,
    pub skip_nginx_validate: bool,
    pub edge_render_interval_seconds: u64,
    pub doh_usage_retention_days: i64,
    pub querylog_mirror_admin_cookie_ttl_seconds: u64,
    pub conveyor_head_page_limit: usize,
    pub conveyor_delete_batch_size: usize,
    pub conveyor_vacuum_pages_active: usize,
    pub live_seed_queue_limit: usize,
    pub identity_cache_ttl_seconds: u64,
    pub identity_cache_max_entries: usize,
    pub filter_cache_ttl_seconds: u64,
    pub filter_cache_max_entries: usize,
    pub doh_hint_cache_ttl_seconds: u64,
    pub doh_hint_cache_max_entries: usize,
    pub dot_session_cache_ttl_seconds: u64,
    pub dot_session_cache_max_entries: usize,
    pub skip_session_check: bool,
}

impl RuntimeConfig {
    pub fn from_env() -> Self {
        load_runtime_env_defaults();

        let dns_dir = path_env("ARBUZAS_DNS_DIR", "/etc/arbuzas/dns");
        let state_dir = path_env("ARBUZAS_DNS_STATE_DIR", "/srv/arbuzas/dns/state");
        let runtime_dir = path_env("ARBUZAS_DNS_RUNTIME_DIR", "/srv/arbuzas/dns/runtime");
        let log_dir = path_env("ARBUZAS_DNS_LOG_DIR", "/srv/arbuzas/dns/logs");
        let run_dir = path_env("ARBUZAS_DNS_RUN_DIR", "/run/arbuzas/dns");
        let controlplane_db = path_env(
            "ARBUZAS_DNS_CONTROLPLANE_DB_FILE",
            &state_dir.join("controlplane.sqlite").display().to_string(),
        );
        let hostname = string_env_alias(
            "ARBUZAS_DNS_HOSTNAME",
            &["PIHOLE_REMOTE_HOSTNAME"],
            "dns.example.com",
        );
        let dot_hostname = string_env_alias(
            "ARBUZAS_DNS_DOT_HOSTNAME",
            &["PIHOLE_REMOTE_DOT_HOSTNAME"],
            &hostname,
        );

        Self {
            runtime_env_file: resolve_runtime_env_file(),
            dns_dir: dns_dir.clone(),
            state_dir: state_dir.clone(),
            runtime_dir: runtime_dir.clone(),
            log_dir,
            run_dir: run_dir.clone(),
            controlplane_socket: path_env(
                "ARBUZAS_DNS_CONTROLPLANE_SOCKET_FILE",
                &run_dir.join("controlplane.sock").display().to_string(),
            ),
            controlplane_db,
            shadow_identities_file: path_env(
                "ARBUZAS_DNS_IDENTITIES_FILE",
                &dns_dir.join("doh-identities.json").display().to_string(),
            ),
            shadow_querylog_view_preference_file: path_env(
                "ARBUZAS_DNS_QUERYLOG_VIEW_PREFERENCE_FILE",
                &state_dir
                    .join("querylog-view-preference.json")
                    .display()
                    .to_string(),
            ),
            legacy_observability_db_file: path_env(
                "ARBUZAS_DNS_LEGACY_OBSERVABILITY_DB_FILE",
                &state_dir
                    .join("identity-observability.sqlite")
                    .display()
                    .to_string(),
            ),
            adguard_querylog_dir: path_env(
                "ARBUZAS_DNS_ADGUARD_QUERYLOG_DIR",
                &state_dir.join("adguard-querylog").display().to_string(),
            ),
            publisher_state_file: path_env(
                "ARBUZAS_DNS_POLICY_PUBLISHER_STATE_FILE",
                &state_dir
                    .join("policy-publisher-state.json")
                    .display()
                    .to_string(),
            ),
            publisher_drift_file: path_env(
                "ARBUZAS_DNS_POLICY_PUBLISHER_DRIFT_FILE",
                &state_dir
                    .join("policy-publisher-drift.json")
                    .display()
                    .to_string(),
            ),
            publisher_health_file: path_env(
                "ARBUZAS_DNS_POLICY_PUBLISHER_HEALTH_FILE",
                &state_dir
                    .join("policy-publisher-health.json")
                    .display()
                    .to_string(),
            ),
            source_config_file: path_env(
                "ARBUZAS_DNS_SOURCE_CONFIG_FILE",
                "/etc/arbuzas/dns/arbuzas-dns.yaml",
            ),
            compiled_policy_file: path_env(
                "ARBUZAS_DNS_COMPILED_POLICY_FILE",
                &runtime_dir
                    .join("compiled-policy.json")
                    .display()
                    .to_string(),
            ),
            rendered_config_file: path_env(
                "ARBUZAS_DNS_RENDERED_CONFIG_FILE",
                "/srv/arbuzas/dns/adguardhome/conf/AdGuardHome.yaml",
            ),
            nginx_template_file: path_env(
                "ARBUZAS_DNS_NGINX_TEMPLATE_FILE",
                "/usr/local/share/arbuzas-dns/arbuzas-dns-nginx.conf.template",
            ),
            nginx_conf_file: path_env(
                "ARBUZAS_DNS_NGINX_CONF_FILE",
                &runtime_dir
                    .join("arbuzas-dns-nginx.conf")
                    .display()
                    .to_string(),
            ),
            nginx_state_file: path_env(
                "ARBUZAS_DNS_EDGE_STATE_FILE",
                &runtime_dir
                    .join("arbuzas-dns-edge-state.json")
                    .display()
                    .to_string(),
            ),
            tls_cert_file: path_env_alias(
                "ARBUZAS_DNS_TLS_CERT_FILE",
                &["PIHOLE_REMOTE_TLS_CERT_FILE"],
                &dns_dir.join("tls/fullchain.pem").display().to_string(),
            ),
            tls_key_file: path_env_alias(
                "ARBUZAS_DNS_TLS_KEY_FILE",
                &["PIHOLE_REMOTE_TLS_KEY_FILE"],
                &dns_dir.join("tls/privkey.pem").display().to_string(),
            ),
            hostname,
            dot_hostname,
            dns_bind_host: string_env("ARBUZAS_DNS_BIND_HOST", "0.0.0.0"),
            dns_port: u16_env("ARBUZAS_DNS_PORT", 53),
            https_port: u16_env_alias("ARBUZAS_DNS_HTTPS_PORT", &["PIHOLE_REMOTE_HTTPS_PORT"], 443),
            dot_port: u16_env_alias("ARBUZAS_DNS_DOT_PORT", &["PIHOLE_REMOTE_DOT_PORT"], 853),
            adguard_web_host: string_env("PIHOLE_WEB_HOST", "127.0.0.1"),
            adguard_web_port: u16_env("PIHOLE_WEB_PORT", 8080),
            admin_username: string_env_alias(
                "ARBUZAS_DNS_ADMIN_USERNAME",
                &["ADGUARDHOME_ADMIN_USERNAME"],
                "pihole",
            ),
            admin_password_file: path_env_alias(
                "ARBUZAS_DNS_ADMIN_PASSWORD_FILE",
                &["ADGUARDHOME_ADMIN_PASSWORD_FILE"],
                "/etc/arbuzas/dns/secrets/admin-password",
            ),
            admin_session_ttl_seconds: u64_env(
                "ARBUZAS_DNS_ADMIN_SESSION_TTL_SECONDS",
                24 * 60 * 60,
            ),
            adguard_admin_username: string_env("ADGUARDHOME_ADMIN_USERNAME", "pihole"),
            adguard_admin_password_file: path_env(
                "ADGUARDHOME_ADMIN_PASSWORD_FILE",
                "/etc/arbuzas/dns/secrets/admin-password",
            ),
            controlplane_host: string_env("ARBUZAS_DNS_CONTROLPLANE_HOST", "0.0.0.0"),
            controlplane_port: u16_env("ARBUZAS_DNS_CONTROLPLANE_PORT", 8097),
            dot_backend_host: string_env("ADGUARDHOME_REMOTE_DOT_INTERNAL_HOST", "127.0.0.1"),
            dot_backend_port: u16_env("ADGUARDHOME_REMOTE_DOT_INTERNAL_PORT", 8853),
            dot_enabled: env_flag_alias(
                "ARBUZAS_DNS_DOT_ENABLED",
                &["PIHOLE_REMOTE_DOT_ENABLED"],
                true,
            ),
            dot_identity_enabled: env_flag_alias(
                "ARBUZAS_DNS_DOT_IDENTITY_ENABLED",
                &["PIHOLE_REMOTE_DOT_IDENTITY_ENABLED"],
                true,
            ),
            router_attr_enabled: env_flag_alias(
                "ARBUZAS_DNS_ROUTER_PUBLIC_IP_ATTRIBUTION_ENABLED",
                &["PIHOLE_REMOTE_ROUTER_PUBLIC_IP_ATTRIBUTION_ENABLED"],
                false,
            ),
            router_lan_ip: optional_string_env_alias(
                "ARBUZAS_DNS_ROUTER_LAN_IP",
                &["PIHOLE_REMOTE_ROUTER_LAN_IP"],
            ),
            ddns_last_ipv4_file: path_env_alias(
                "ARBUZAS_DNS_DDNS_LAST_IPV4_FILE",
                &["PIHOLE_DDNS_LAST_IPV4_FILE"],
                &state_dir.join("ddns-last-ipv4").display().to_string(),
            ),
            nginx_bin: optional_string_env("ARBUZAS_DNS_NGINX_BIN")
                .or_else(|| optional_string_env("NGINX_BIN")),
            nginx_prefix: string_env("ARBUZAS_DNS_NGINX_PREFIX", "/usr/share/nginx"),
            skip_nginx_validate: env_flag("ARBUZAS_DNS_SKIP_NGINX_VALIDATE", false),
            edge_render_interval_seconds: u64_env("ARBUZAS_DNS_RENDER_INTERVAL_SECONDS", 20),
            doh_usage_retention_days: i64_env("ARBUZAS_DNS_USAGE_RETENTION_DAYS", 30),
            querylog_mirror_admin_cookie_ttl_seconds: u64_env(
                "ARBUZAS_DNS_QUERYLOG_ADMIN_COOKIE_TTL_SECONDS",
                300,
            ),
            conveyor_head_page_limit: usize_env("ARBUZAS_DNS_CONVEYOR_HEAD_PAGE_LIMIT", 250).max(1),
            conveyor_delete_batch_size: usize_env("ARBUZAS_DNS_CONVEYOR_DELETE_BATCH_SIZE", 1000)
                .max(1),
            conveyor_vacuum_pages_active: usize_env(
                "ARBUZAS_DNS_CONVEYOR_VACUUM_PAGES_ACTIVE",
                128,
            ),
            live_seed_queue_limit: usize_env("ARBUZAS_DNS_LIVE_SEED_QUEUE_LIMIT", 1024).max(1),
            identity_cache_ttl_seconds: u64_env("ARBUZAS_DNS_IDENTITY_CACHE_TTL_SECONDS", 30)
                .max(1),
            identity_cache_max_entries: usize_env("ARBUZAS_DNS_IDENTITY_CACHE_MAX_ENTRIES", 512)
                .max(1),
            filter_cache_ttl_seconds: u64_env("ARBUZAS_DNS_FILTER_CACHE_TTL_SECONDS", 300).max(1),
            filter_cache_max_entries: usize_env("ARBUZAS_DNS_FILTER_CACHE_MAX_ENTRIES", 256).max(1),
            doh_hint_cache_ttl_seconds: u64_env("ARBUZAS_DNS_DOH_HINT_CACHE_TTL_SECONDS", 15 * 60)
                .max(1),
            doh_hint_cache_max_entries: usize_env("ARBUZAS_DNS_DOH_HINT_CACHE_MAX_ENTRIES", 2048)
                .max(1),
            dot_session_cache_ttl_seconds: u64_env(
                "ARBUZAS_DNS_DOT_SESSION_CACHE_TTL_SECONDS",
                15 * 60,
            )
            .max(1),
            dot_session_cache_max_entries: usize_env(
                "ARBUZAS_DNS_DOT_SESSION_CACHE_MAX_ENTRIES",
                2048,
            )
            .max(1),
            skip_session_check: env_flag("ARBUZAS_DNS_SKIP_SESSION_CHECK", false),
        }
    }

    pub fn ensure_runtime_dirs(&self) -> Result<()> {
        for path in [
            self.state_dir.as_path(),
            self.runtime_dir.as_path(),
            self.run_dir.as_path(),
            self.adguard_querylog_dir.as_path(),
        ] {
            fs::create_dir_all(path)
                .with_context(|| format!("create directory {}", path.display()))?;
        }
        Ok(())
    }
}

pub fn resolve_runtime_env_file() -> PathBuf {
    for candidate in [
        env::var("ARBUZAS_DNS_RUNTIME_ENV_FILE").ok(),
        env::var("PIHOLE_REMOTE_RUNTIME_ENV_FILE").ok(),
    ]
    .into_iter()
    .flatten()
    {
        let candidate = PathBuf::from(candidate.trim());
        if !candidate.as_os_str().is_empty() {
            return candidate;
        }
    }

    for fallback in [
        "/etc/arbuzas/dns/runtime.env",
        "/srv/arbuzas/dns/runtime.env",
    ] {
        let candidate = PathBuf::from(fallback);
        if candidate.is_file() {
            return candidate;
        }
    }

    PathBuf::from("/etc/arbuzas/dns/runtime.env")
}

pub fn load_runtime_env_defaults() {
    let path = resolve_runtime_env_file();
    let Ok(raw) = fs::read_to_string(&path) else {
        return;
    };

    for raw_line in raw.lines() {
        let line = raw_line.trim();
        if line.is_empty() || line.starts_with('#') || !line.contains('=') {
            continue;
        }
        let (raw_key, raw_value) = line.split_once('=').unwrap_or_default();
        let key = raw_key.trim();
        if key.is_empty() || env::var_os(key).is_some() {
            continue;
        }
        let value = raw_value.trim().trim_matches('"').trim_matches('\'');
        env::set_var(key, value);
    }
}

pub fn env_flag(name: &str, default: bool) -> bool {
    match env::var(name) {
        Ok(value) => matches!(
            value.trim().to_ascii_lowercase().as_str(),
            "1" | "true" | "yes" | "on"
        ),
        Err(_) => default,
    }
}

pub fn parse_duration_seconds(value: &str, default_seconds: i64) -> Result<i64> {
    let raw = value.trim().to_ascii_lowercase();
    if raw.is_empty() {
        return Ok(default_seconds);
    }
    if let Ok(seconds) = raw.parse::<i64>() {
        return Ok(seconds.max(1));
    }
    let number: i64 = raw[..raw.len().saturating_sub(1)]
        .parse()
        .with_context(|| format!("parse duration number from {value}"))?;
    let suffix = raw.chars().last().unwrap_or('s');
    let multiplier = match suffix {
        's' => 1,
        'm' => 60,
        'h' => 3600,
        'd' => 86_400,
        'w' => 7 * 86_400,
        _ => anyhow::bail!("unsupported duration suffix in {value}"),
    };
    Ok((number * multiplier).max(1))
}

pub fn write_atomic(path: &Path, content: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .with_context(|| format!("create parent directory for {}", path.display()))?;
    }
    let tmp = path.with_extension(
        path.extension()
            .map(|ext| format!("{}.tmp", ext.to_string_lossy()))
            .unwrap_or_else(|| "tmp".to_string()),
    );
    fs::write(&tmp, content).with_context(|| format!("write temp file {}", tmp.display()))?;
    fs::rename(&tmp, path)
        .with_context(|| format!("rename {} to {}", tmp.display(), path.display()))?;
    Ok(())
}

fn string_env(name: &str, default: &str) -> String {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| default.to_string())
}

fn string_env_alias(name: &str, aliases: &[&str], default: &str) -> String {
    optional_string_env_alias(name, aliases).unwrap_or_else(|| default.to_string())
}

fn optional_string_env(name: &str) -> Option<String> {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
}

fn optional_string_env_alias(name: &str, aliases: &[&str]) -> Option<String> {
    optional_string_env(name)
        .or_else(|| aliases.iter().find_map(|alias| optional_string_env(alias)))
}

fn path_env(name: &str, default: &str) -> PathBuf {
    PathBuf::from(string_env(name, default))
}

fn path_env_alias(name: &str, aliases: &[&str], default: &str) -> PathBuf {
    PathBuf::from(string_env_alias(name, aliases, default))
}

fn u16_env(name: &str, default: u16) -> u16 {
    env::var(name)
        .ok()
        .and_then(|value| value.trim().parse::<u16>().ok())
        .unwrap_or(default)
}

fn u16_env_alias(name: &str, aliases: &[&str], default: u16) -> u16 {
    optional_string_env_alias(name, aliases)
        .and_then(|value| value.parse::<u16>().ok())
        .unwrap_or(default)
}

fn u64_env(name: &str, default: u64) -> u64 {
    env::var(name)
        .ok()
        .and_then(|value| value.trim().parse::<u64>().ok())
        .unwrap_or(default)
}

fn i64_env(name: &str, default: i64) -> i64 {
    env::var(name)
        .ok()
        .and_then(|value| value.trim().parse::<i64>().ok())
        .unwrap_or(default)
}

fn usize_env(name: &str, default: usize) -> usize {
    env::var(name)
        .ok()
        .and_then(|value| value.trim().parse::<usize>().ok())
        .unwrap_or(default)
}

fn env_flag_alias(name: &str, aliases: &[&str], default: bool) -> bool {
    optional_string_env_alias(name, aliases)
        .map(|value| {
            matches!(
                value.trim().to_ascii_lowercase().as_str(),
                "1" | "true" | "yes" | "on"
            )
        })
        .unwrap_or(default)
}
