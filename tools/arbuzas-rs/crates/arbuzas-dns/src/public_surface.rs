use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::net::{IpAddr, SocketAddr, ToSocketAddrs};

use anyhow::{anyhow, Context, Result};
use arbuzas_dns_lib::config::{
    parse_duration_seconds, QUERYLOG_DEFAULT_VIEW_IMPROVED, QUERYLOG_DEFAULT_VIEW_NATIVE,
};
use arbuzas_dns_lib::state::{
    normalize_identity_id, now_epoch_seconds, usage_from_observability, QuerylogDetailLevel,
    QuerylogMirrorQueryResult, QuerylogMirrorRow,
};
use axum::body::{Body, Bytes};
use axum::extract::{ConnectInfo, OriginalUri, State};
use axum::http::{HeaderMap, HeaderName, Method, StatusCode};
use axum::response::{IntoResponse, Response};
use percent_encoding::{percent_decode_str, utf8_percent_encode, NON_ALPHANUMERIC};
use reqwest::header::COOKIE;
use serde::{Deserialize, Serialize};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256};
use url::form_urlencoded;
use url::Url;

use crate::whois_cache::cached_whois_info;
use crate::AppState;

const IDENTITY_HTML: &str = include_str!("identity_assets/identity.html");
const LOGIN_HTML: &str = include_str!("identity_assets/login.html");
const OVERVIEW_HTML: &str = include_str!("identity_assets/overview.html");
const CLIENTS_HTML: &str = include_str!("identity_assets/clients.html");
const SETTINGS_HTML: &str = include_str!("identity_assets/settings.html");
const QUERYLOG_HTML: &str = include_str!("identity_assets/querylog.html");
const DOT_IDENTITY_LABEL_LENGTH: i64 = 20;
const QUERYLOG_LIMIT_DEFAULT: usize = 1000;
const QUERYLOG_LIMIT_MIN: usize = 1;
const QUERYLOG_LIMIT_MAX: usize = 10000;
const QUERYLOG_PAGE_SIZE_DEFAULT: usize = 500;
const INTERNAL_QUERYLOG_CLIENTS_DEFAULT: &str = "127.0.0.1,::1";
const INTERNAL_PROBE_DOMAINS_DEFAULT: &str = "example.com";
const ADMIN_SESSION_COOKIE: &str = "arbuzas_dns_admin_session";
const ADMIN_NAV_PLACEHOLDER: &str = "__ADMIN_NAV__";
const ADMIN_BASE_PATH: &str = "/dns";
const ADMIN_LOGIN_PATH: &str = "/dns/login";
const ADMIN_API_BASE_PATH: &str = "/dns/api";
const LOGIN_RETURN_DEFAULT: &str = "/dns";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum AdminNavItem {
    Overview,
    Settings,
    Clients,
    Identities,
    Queries,
}

fn retired_admin_base_paths() -> [String; 2] {
    [legacy_admin_base_path("dns"), legacy_admin_base_path("identity")]
}

fn legacy_admin_base_path(suffix: &str) -> String {
    format!("/{}-stack/{suffix}", legacy_stack_label())
}

fn legacy_stack_label() -> String {
    [112_u8, 105, 120, 101, 108]
        .into_iter()
        .map(char::from)
        .collect()
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct SettingsEditablePayload {
    policy: SettingsPolicyPayload,
    runtime: SettingsRuntimePayload,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct SettingsPolicyPayload {
    upstreams: Vec<String>,
    filters: Vec<crate::native_runtime::SourceFilterEntry>,
    #[serde(rename = "whitelistFilters")]
    whitelist_filters: Vec<crate::native_runtime::SourceFilterEntry>,
    #[serde(rename = "userRules")]
    user_rules: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct SettingsRuntimePayload {
    #[serde(rename = "usageRetentionDays")]
    usage_retention_days: i64,
    #[serde(rename = "querylogDefaultView")]
    querylog_default_view: String,
}

pub async fn handle_public_request(
    State(state): State<AppState>,
    method: Method,
    headers: HeaderMap,
    uri: OriginalUri,
    body: Bytes,
) -> Response {
    let result = dispatch_public_request(&state, &method, &headers, &uri, None, body).await;
    match result {
        Ok(response) => response,
        Err(error) => json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({ "error": error.to_string() }),
        ),
    }
}

pub async fn handle_public_request_with_connect_info(
    State(state): State<AppState>,
    ConnectInfo(remote_addr): ConnectInfo<SocketAddr>,
    method: Method,
    headers: HeaderMap,
    uri: OriginalUri,
    body: Bytes,
) -> Response {
    let result =
        dispatch_public_dns_request(&state, &method, &headers, &uri, remote_addr, body).await;
    match result {
        Ok(response) => response,
        Err(error) => json_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            json!({ "error": error.to_string() }),
        ),
    }
}

async fn dispatch_public_dns_request(
    state: &AppState,
    method: &Method,
    headers: &HeaderMap,
    uri: &OriginalUri,
    remote_addr: SocketAddr,
    body: Bytes,
) -> Result<Response> {
    let path = uri.path();
    if crate::native_runtime::doh_path_matches(path) {
        return crate::native_runtime::handle_doh_request(
            state,
            method,
            headers,
            uri,
            body,
            Some(remote_addr),
        )
        .await;
    }
    Ok((StatusCode::NOT_FOUND, "not found").into_response())
}

async fn dispatch_public_request(
    state: &AppState,
    method: &Method,
    headers: &HeaderMap,
    uri: &OriginalUri,
    remote_addr: Option<SocketAddr>,
    body: Bytes,
) -> Result<Response> {
    let request_is_secure = remote_addr.is_some();
    let path = uri.path();
    if retired_admin_base_paths()
        .iter()
        .any(|prefix| path == prefix || path.starts_with(&format!("{prefix}/")))
    {
        return Ok((StatusCode::GONE, "gone").into_response());
    }
    if path == "/" || path == "/index.html" {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(if session_authenticated(state, headers).await? {
                    ADMIN_BASE_PATH.to_string()
                } else {
                    ADMIN_LOGIN_PATH.to_string()
                })
            }
            _ => method_not_allowed(),
        });
    }
    if path == "/login" || path == "/login.html" {
        return Ok(match *method {
            Method::GET | Method::HEAD => redirect_response(ADMIN_LOGIN_PATH.to_string()),
            _ => method_not_allowed(),
        });
    }
    if crate::native_runtime::doh_path_matches(path) {
        return crate::native_runtime::handle_doh_request(
            state,
            method,
            headers,
            uri,
            body,
            remote_addr,
        )
        .await;
    }
    if path == ADMIN_LOGIN_PATH {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                if session_authenticated(state, headers).await? {
                    redirect_response(ADMIN_BASE_PATH.to_string())
                } else {
                    text_response(
                        StatusCode::OK,
                        "text/html; charset=utf-8",
                        LOGIN_HTML.to_string(),
                    )
                }
            }
            _ => method_not_allowed(),
        });
    }

    if path == ADMIN_BASE_PATH || path == format!("{ADMIN_BASE_PATH}/") {
        if *method != Method::GET && *method != Method::HEAD {
            return Ok(method_not_allowed());
        }
        if let Some(response) = require_session(state, headers, false).await? {
            return Ok(response);
        }
        if path.ends_with('/') {
            return Ok(redirect_response(ADMIN_BASE_PATH.to_string()));
        }
        return Ok(text_response(
            StatusCode::OK,
            "text/html; charset=utf-8",
            render_admin_asset(OVERVIEW_HTML, AdminNavItem::Overview),
        ));
    }

    if path == format!("{ADMIN_BASE_PATH}/settings") {
        if *method != Method::GET && *method != Method::HEAD {
            return Ok(method_not_allowed());
        }
        if let Some(response) = require_session(state, headers, false).await? {
            return Ok(response);
        }
        return Ok(text_response(
            StatusCode::OK,
            "text/html; charset=utf-8",
            render_admin_asset(SETTINGS_HTML, AdminNavItem::Settings),
        ));
    }

    if path == format!("{ADMIN_BASE_PATH}/filters") {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(format!("{ADMIN_BASE_PATH}/settings#filters"))
            }
            _ => method_not_allowed(),
        });
    }

    if path == format!("{ADMIN_BASE_PATH}/encryption") {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(format!("{ADMIN_BASE_PATH}/settings#encryption"))
            }
            _ => method_not_allowed(),
        });
    }

    if path == format!("{ADMIN_BASE_PATH}/access") {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(format!("{ADMIN_BASE_PATH}/settings#access"))
            }
            _ => method_not_allowed(),
        });
    }

    if path == format!("{ADMIN_BASE_PATH}/rewrites") {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(format!("{ADMIN_BASE_PATH}/settings#rewrites"))
            }
            _ => method_not_allowed(),
        });
    }

    if path == format!("{ADMIN_BASE_PATH}/dhcp") {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(format!("{ADMIN_BASE_PATH}/settings#dhcp"))
            }
            _ => method_not_allowed(),
        });
    }

    if path == format!("{ADMIN_BASE_PATH}/setup") {
        return Ok(match *method {
            Method::GET | Method::HEAD => {
                redirect_response(format!("{ADMIN_BASE_PATH}/identities#setup"))
            }
            _ => method_not_allowed(),
        });
    }

    if path == format!("{ADMIN_BASE_PATH}/clients") {
        if *method != Method::GET && *method != Method::HEAD {
            return Ok(method_not_allowed());
        }
        if let Some(response) = require_session(state, headers, false).await? {
            return Ok(response);
        }
        return Ok(text_response(
            StatusCode::OK,
            "text/html; charset=utf-8",
            render_admin_asset(CLIENTS_HTML, AdminNavItem::Clients),
        ));
    }

    if path == format!("{ADMIN_BASE_PATH}/identities") {
        if *method != Method::GET && *method != Method::HEAD {
            return Ok(method_not_allowed());
        }
        if let Some(response) = require_session(state, headers, false).await? {
            return Ok(response);
        }
        let preference = state.db.querylog_view_preference()?;
        return Ok(text_response(
            StatusCode::OK,
            "text/html; charset=utf-8",
            render_identity_page(
                IDENTITY_HTML,
                AdminNavItem::Identities,
                &preference.default_view,
                &preference.updated_at,
            ),
        ));
    }

    if path == format!("{ADMIN_BASE_PATH}/queries") {
        if *method != Method::GET && *method != Method::HEAD {
            return Ok(method_not_allowed());
        }
        if let Some(response) = require_session(state, headers, false).await? {
            return Ok(response);
        }
        return Ok(text_response(
            StatusCode::OK,
            "text/html; charset=utf-8",
            render_admin_asset(QUERYLOG_HTML, AdminNavItem::Queries),
        ));
    }

    if path == format!("{ADMIN_BASE_PATH}/stats") {
        return Ok(match *method {
            Method::GET | Method::HEAD => redirect_response(ADMIN_BASE_PATH.to_string()),
            _ => method_not_allowed(),
        });
    }

    if !path.starts_with(ADMIN_API_BASE_PATH) {
        return Ok((StatusCode::NOT_FOUND, "not found").into_response());
    }
    if path != format!("{ADMIN_API_BASE_PATH}/session") {
        if let Some(response) = require_session(state, headers, true).await? {
            return Ok(response);
        }
    }

    let query = parse_query(uri.query().unwrap_or_default());
    match *method {
        Method::GET | Method::HEAD => handle_api_get(state, headers, path, &query).await,
        Method::POST => {
            if requires_same_origin(path) && !same_origin_valid(headers, request_is_secure) {
                return Ok(json_response(
                    StatusCode::FORBIDDEN,
                    json!({ "error": "Origin validation failed." }),
                ));
            }
            handle_api_post(state, headers, path, &query, body, request_is_secure).await
        }
        Method::DELETE => {
            if requires_same_origin(path) && !same_origin_valid(headers, request_is_secure) {
                return Ok(json_response(
                    StatusCode::FORBIDDEN,
                    json!({ "error": "Origin validation failed." }),
                ));
            }
            handle_api_delete(state, headers, path, request_is_secure).await
        }
        _ => Ok(method_not_allowed()),
    }
}

async fn handle_api_get(
    state: &AppState,
    headers: &HeaderMap,
    path: &str,
    query: &BTreeMap<String, Vec<String>>,
) -> Result<Response> {
    let apple_prefix = format!("{ADMIN_API_BASE_PATH}/identities/");
    for suffix in ["/apple-doh.mobileconfig", "/apple-dot.mobileconfig"] {
        if path.starts_with(&apple_prefix)
            && path.ends_with(suffix)
            && path.len() > apple_prefix.len() + suffix.len()
        {
            let raw_identity = &path[apple_prefix.len()..path.len() - suffix.len()];
            let identity_id = decode_path_segment(raw_identity)?;
            let payload = identities_payload(state)?;
            let entry = find_identity_entry(&payload, &identity_id)
                .ok_or_else(|| anyhow!("Identity not found."))?;
            let profile = if suffix.contains("apple-doh") {
                build_apple_doh_profile(&entry, headers)?
            } else {
                build_apple_dot_profile(&entry, headers)?
            };
            return Ok(bytes_response(
                StatusCode::OK,
                "application/x-apple-aspen-config",
                profile.0.into_bytes(),
                &[(
                    "Content-Disposition",
                    format!("attachment; filename=\"{}\"", profile.1),
                )],
            ));
        }
    }

    let query_row_prefix = format!("{ADMIN_API_BASE_PATH}/queries/");
    if path.starts_with(&query_row_prefix) && path.len() > query_row_prefix.len() {
        let row_fingerprint = decode_path_segment(&path[query_row_prefix.len()..])?;
        let Some(row) = state
            .db
            .querylog_mirror_row_by_fingerprint(&row_fingerprint, QuerylogDetailLevel::Full)?
        else {
            return Ok(json_response(
                StatusCode::NOT_FOUND,
                json!({ "error": "Query row not found." }),
            ));
        };
        return Ok(json_response(
            StatusCode::OK,
            normalize_querylog_row(&row, QuerylogDetailLevel::Full),
        ));
    }

    match path {
        "/dns/api/session" => Ok(json_response(
            StatusCode::OK,
            admin_session_payload(state, headers).await?,
        )),
        "/dns/api/settings" => Ok(json_response(StatusCode::OK, settings_payload(state)?)),
        "/dns/api/identities" => Ok(json_response(StatusCode::OK, identities_payload(state)?)),
        "/dns/api/usage" => {
            let identity = first_query_value(query, "identity");
            let window = first_query_value(query, "window");
            Ok(json_response(
                StatusCode::OK,
                usage_payload(state, &identity, &window)?,
            ))
        }
        "/dns/api/queries" => {
            if matches!(
                first_query_value(query, "summary").to_ascii_lowercase().as_str(),
                "1" | "true" | "yes"
            ) {
                let view_mode = parse_querylog_view(first_query_value(query, "view"))?;
                let limit = parse_querylog_limit(first_query_value(query, "limit"))?;
                return Ok(json_response(
                    StatusCode::OK,
                    querylog_summary_payload(state, limit, &view_mode)?,
                ));
            }

            let identity_filter = first_query_value(query, "identity");
            let detail_level = parse_querylog_detail_level(&first_query_value(query, "detail"))?;
            if detail_level == QuerylogDetailLevel::Full {
                return Ok(json_response(
                    StatusCode::BAD_REQUEST,
                    json!({
                        "error": "Paginated query results only return summary rows. Fetch /dns/api/queries/{rowFingerprint} for full row detail.",
                    }),
                ));
            }
            let response_status =
                normalize_querylog_response_status(&first_query_value(query, "response_status"));
            let cursor = first_query_value(query, "cursor");
            let page_size = parse_querylog_page_size(first_query_value(query, "page_size"))?;
            let result = state.db.querylog_mirror_page_summary(
                option_string(&identity_filter).as_deref(),
                option_string(&cursor).as_deref(),
                option_string(&first_query_value(query, "search")).as_deref(),
                &response_status,
                page_size,
            )?;
            Ok(json_response(
                StatusCode::OK,
                querylog_proxy_payload(result, &identity_filter, detail_level),
            ))
        }
        "/dns/api/stats" => Ok(json_response(
            StatusCode::OK,
            stats_payload_from_querylog(state, query)?,
        )),
        "/dns/api/clients" => Ok(json_response(
            StatusCode::OK,
            clients_payload_from_querylog(state, &first_query_value(query, "search"))?,
        )),
        _ => Ok(json_response(
            StatusCode::NOT_FOUND,
            json!({ "error": "Not found" }),
        )),
    }
}

async fn handle_api_post(
    state: &AppState,
    _headers: &HeaderMap,
    path: &str,
    _query: &BTreeMap<String, Vec<String>>,
    body: Bytes,
    request_is_secure: bool,
) -> Result<Response> {
    match path {
        "/dns/api/session" => {
            let payload = parse_json_object(&body)?;
            let username = payload
                .get("username")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .trim();
            let password = payload
                .get("password")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let return_target = normalize_return_target(
                payload
                    .get("returnTo")
                    .and_then(Value::as_str)
                    .unwrap_or(LOGIN_RETURN_DEFAULT)
                    .to_string(),
            );
            if !admin_credentials_match(state, username, password)? {
                return Ok(json_response(
                    StatusCode::UNAUTHORIZED,
                    json!({ "error": "Invalid username or password." }),
                ));
            }
            let session = state
                .write_serialized(|db| {
                    db.create_admin_session(state.config.admin_session_ttl_seconds)
                })
                .await?;
            return Ok(json_response_with_headers(
                StatusCode::OK,
                json!({
                    "authenticated": true,
                    "username": state.config.admin_username,
                    "expiresEpochSeconds": session.expires_epoch_seconds,
                    "returnTo": return_target,
                }),
                &[(
                    "Set-Cookie",
                    session_cookie_value(
                        &session.token,
                        state.config.admin_session_ttl_seconds,
                        request_is_secure,
                    ),
                )],
            ));
        }
        "/dns/api/settings/validate" => {
            let payload = parse_json_object(&body)?;
            return Ok(match validate_settings_payload(state, payload).await {
                Ok(validated) => json_response(StatusCode::OK, validated),
                Err(error) => json_response(
                    StatusCode::BAD_REQUEST,
                    json!({ "error": error.to_string() }),
                ),
            });
        }
        "/dns/api/settings/apply" => {
            let payload = parse_json_object(&body)?;
            return Ok(match apply_settings_payload(state, payload).await {
                Ok(applied) => json_response(StatusCode::OK, applied),
                Err(error) => json_response(
                    StatusCode::BAD_REQUEST,
                    json!({ "error": error.to_string() }),
                ),
            });
        }
        "/dns/api/identities" => {
            let payload = parse_json_object(&body)?;
            let identity_id = payload
                .get("id")
                .and_then(Value::as_str)
                .unwrap_or_default();
            let token = payload.get("token").and_then(Value::as_str);
            let primary = payload
                .get("primary")
                .and_then(Value::as_bool)
                .unwrap_or(false);
            let expires_epoch_seconds = parse_optional_epoch(payload.get("expiresEpochSeconds"))?;
            if expires_epoch_seconds
                .map(|value| value <= now_epoch_seconds())
                .unwrap_or(false)
            {
                return Ok(json_response(
                    StatusCode::BAD_REQUEST,
                    json!({ "error": "Invalid expiresEpochSeconds: must be in the future." }),
                ));
            }
            let created = state
                .write_serialized(|db| {
                    db.create_identity(
                        &state.config,
                        identity_id,
                        token,
                        primary,
                        expires_epoch_seconds,
                    )
                })
                .await;
            return match created {
                Ok(payload) => {
                    state.sync_trigger.invalidate_identity_cache();
                    Ok(json_response(StatusCode::OK, payload))
                }
                Err(error) => Ok(json_response(
                    StatusCode::BAD_REQUEST,
                    json!({ "error": error.to_string() }),
                )),
            };
        }
        _ => {}
    }

    let rename_prefix = "/dns/api/identities/";
    let rename_suffix = "/rename";
    if path.starts_with(rename_prefix)
        && path.ends_with(rename_suffix)
        && path.len() > rename_prefix.len() + rename_suffix.len()
    {
        let identity_id =
            decode_path_segment(&path[rename_prefix.len()..path.len() - rename_suffix.len()])?;
        let payload = parse_json_object(&body)?;
        let next_id = payload
            .get("newId")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase();
        if next_id.is_empty() {
            return Ok(json_response(
                StatusCode::BAD_REQUEST,
                json!({ "error": "Invalid new identity id. Expected lower-case slug value (1-64 chars)." }),
            ));
        }
        let renamed = state
            .write_serialized(|db| db.rename_identity(&state.config, &identity_id, &next_id))
            .await;
        return match renamed {
            Ok(payload) => {
                state.sync_trigger.invalidate_identity_cache();
                Ok(json_response(StatusCode::OK, payload))
            }
            Err(error) => Ok(json_response(
                StatusCode::BAD_REQUEST,
                json!({ "error": error.to_string() }),
            )),
        };
    }

    Ok(json_response(
        StatusCode::NOT_FOUND,
        json!({ "error": "Not found" }),
    ))
}

async fn handle_api_delete(
    state: &AppState,
    headers: &HeaderMap,
    path: &str,
    request_is_secure: bool,
) -> Result<Response> {
    if path == "/dns/api/session" {
        if let Some(token) = admin_session_token(headers) {
            let _ = state
                .write_serialized(|db| db.revoke_admin_session(&token))
                .await?;
        }
        return Ok(json_response_with_headers(
            StatusCode::OK,
            json!({ "authenticated": false }),
            &[(
                "Set-Cookie",
                expired_session_cookie_value(request_is_secure),
            )],
        ));
    }
    let prefix = "/dns/api/identities/";
    if !path.starts_with(prefix) {
        return Ok(json_response(
            StatusCode::NOT_FOUND,
            json!({ "error": "Not found" }),
        ));
    }
    let identity_id = decode_path_segment(&path[prefix.len()..])?;
    let revoked = state
        .write_serialized(|db| db.revoke_identity(&state.config, &identity_id, false))
        .await;
    Ok(match revoked {
        Ok(payload) => {
            state.sync_trigger.invalidate_identity_cache();
            json_response(StatusCode::OK, payload)
        }
        Err(error) => json_response(
            StatusCode::BAD_REQUEST,
            json!({ "error": error.to_string() }),
        ),
    })
}

async fn admin_session_payload(state: &AppState, headers: &HeaderMap) -> Result<Value> {
    let authenticated = if state.config.skip_session_check {
        true
    } else {
        session_authenticated(state, headers).await?
    };
    Ok(json!({
        "authenticated": authenticated,
        "username": if authenticated { state.config.admin_username.clone() } else { String::new() },
    }))
}

fn settings_payload(state: &AppState) -> Result<Value> {
    let source = load_source_config(state)?;
    let runtime = state.runtime_settings();
    let compiled = state.compiled_policy();
    Ok(json!({
        "transport": {
            "hostname": state.config.hostname.clone(),
            "dotHostname": state.config.dot_hostname.clone(),
            "httpsPort": state.config.https_port,
            "dotPort": state.config.dot_port,
            "tlsCertPresent": state.config.tls_cert_file.is_file(),
            "tlsKeyPresent": state.config.tls_key_file.is_file(),
            "sourceConfigFile": state.config.source_config_file.display().to_string(),
            "compiledPolicyFile": state.config.compiled_policy_file.display().to_string(),
        },
        "policy": {
            "upstreams": source.upstreams,
            "filters": source.filters,
            "whitelistFilters": source.whitelist_filters,
            "userRules": source.user_rules,
        },
        "runtime": {
            "usageRetentionDays": runtime.usage_retention_days,
            "querylogDefaultView": runtime.querylog_view_preference.default_view,
            "querylogDefaultViewUpdatedAt": runtime.querylog_view_preference.updated_at,
        },
        "health": {
            "compiledAt": compiled.compiled_at,
            "compiledPolicyHash": compiled.compiled_hash,
            "sourceConfigHash": compiled.source_hash,
            "lastAppliedAt": runtime.last_applied_at,
            "lastAppliedPolicyHash": runtime.last_applied_policy_hash,
        },
    }))
}

async fn validate_settings_payload(state: &AppState, payload: Map<String, Value>) -> Result<Value> {
    let (normalized, prepared) = normalize_settings_candidate(state, payload).await?;
    let compiled = &prepared.compiled;
    Ok(json!({
        "valid": true,
        "policy": serde_json::to_value(&normalized.policy)?,
        "runtime": serde_json::to_value(&normalized.runtime)?,
        "health": {
            "sourceConfigHash": compiled.source_hash.clone(),
            "managedPolicyHash": compiled.compiled_hash.clone(),
            "upstreamCount": compiled.upstreams.len(),
            "allowExactCount": compiled.allow_exact.len(),
            "allowSuffixCount": compiled.allow_suffix.len(),
            "blockExactCount": compiled.block_exact.len(),
            "blockSuffixCount": compiled.block_suffix.len(),
        },
    }))
}

async fn apply_settings_payload(state: &AppState, payload: Map<String, Value>) -> Result<Value> {
    let (normalized, prepared) = normalize_settings_candidate(state, payload).await?;
    let previous_source = fs::read(&state.config.source_config_file).unwrap_or_default();
    let previous_compiled = fs::read(&state.config.compiled_policy_file).unwrap_or_default();
    let previous_runtime = state.runtime_settings();

    let _guard = state.runtime_write_gate.lock().await;
    crate::native_runtime::persist_prepared_compiled_policy(&state.config, &prepared)?;
    let runtime_settings = match state.db.apply_admin_runtime_settings(
        &state.config,
        normalized.runtime.usage_retention_days,
        &normalized.runtime.querylog_default_view,
        &prepared.compiled.compiled_hash,
    ) {
        Ok(settings) => settings,
        Err(error) => {
            restore_previous_settings(
                state,
                &previous_source,
                &previous_compiled,
                &previous_runtime,
            )
            .await?;
            anyhow::bail!("persist runtime settings after apply: {error}");
        }
    };

    state.replace_compiled_policy(std::sync::Arc::new(prepared.compiled.clone()));
    state.replace_runtime_settings(runtime_settings);
    state
        .sync_trigger
        .remember_filter_lookup(&prepared.compiled.filter_lookup)
        .await;

    let mut payload = settings_payload(state)?;
    payload["applied"] = Value::Bool(true);
    payload["applyResult"] = prepared.response_payload(&state.config);
    Ok(payload)
}

async fn restore_previous_settings(
    state: &AppState,
    previous_source: &[u8],
    previous_compiled: &[u8],
    previous_runtime: &arbuzas_dns_lib::state::AdminRuntimeSettings,
) -> Result<()> {
    if !previous_source.is_empty() {
        arbuzas_dns_lib::config::write_atomic(&state.config.source_config_file, previous_source)?;
    }
    if !previous_compiled.is_empty() {
        arbuzas_dns_lib::config::write_atomic(
            &state.config.compiled_policy_file,
            previous_compiled,
        )?;
        if let Ok(policy) =
            serde_json::from_slice::<crate::native_runtime::CompiledPolicy>(previous_compiled)
        {
            state.replace_compiled_policy(std::sync::Arc::new(policy));
        }
    }
    let restored = state.db.apply_admin_runtime_settings(
        &state.config,
        previous_runtime.usage_retention_days,
        &previous_runtime.querylog_view_preference.default_view,
        &previous_runtime.last_applied_policy_hash,
    )?;
    state.replace_runtime_settings(restored);
    Ok(())
}

async fn normalize_settings_candidate(
    state: &AppState,
    payload: Map<String, Value>,
) -> Result<(
    SettingsEditablePayload,
    crate::native_runtime::PreparedCompiledPolicyWrite,
)> {
    let mut candidate: SettingsEditablePayload =
        serde_json::from_value(Value::Object(payload)).context("parse settings payload")?;
    candidate.policy.upstreams = candidate
        .policy
        .upstreams
        .into_iter()
        .map(|value| value.trim().to_string())
        .filter(|value| !value.is_empty())
        .collect();
    if candidate.policy.upstreams.is_empty() {
        anyhow::bail!("At least one upstream is required.");
    }
    normalize_filter_entries(&mut candidate.policy.filters)?;
    normalize_filter_entries(&mut candidate.policy.whitelist_filters)?;
    candidate.policy.user_rules = candidate
        .policy
        .user_rules
        .into_iter()
        .flat_map(|value| {
            value
                .lines()
                .map(str::trim)
                .map(ToString::to_string)
                .collect::<Vec<_>>()
        })
        .filter(|value| !value.is_empty())
        .collect();
    candidate.runtime.usage_retention_days = candidate.runtime.usage_retention_days.max(1);
    if candidate.runtime.usage_retention_days > 3650 {
        anyhow::bail!("Usage retention days must be between 1 and 3650.");
    }
    candidate.runtime.querylog_default_view = candidate
        .runtime
        .querylog_default_view
        .trim()
        .to_ascii_lowercase();
    if candidate.runtime.querylog_default_view != QUERYLOG_DEFAULT_VIEW_NATIVE
        && candidate.runtime.querylog_default_view != QUERYLOG_DEFAULT_VIEW_IMPROVED
    {
        anyhow::bail!("Query log default view must be native or improved.");
    }
    let source = crate::native_runtime::SourceConfig {
        schema_version: Some(1),
        upstreams: candidate.policy.upstreams.clone(),
        filters: candidate.policy.filters.clone(),
        whitelist_filters: candidate.policy.whitelist_filters.clone(),
        user_rules: candidate.policy.user_rules.clone(),
    };
    let prepared =
        crate::native_runtime::prepare_compiled_policy_write(&source, &state.client).await?;
    Ok((candidate, prepared))
}

fn normalize_filter_entries(
    entries: &mut Vec<crate::native_runtime::SourceFilterEntry>,
) -> Result<()> {
    let mut normalized = Vec::new();
    for mut entry in entries.drain(..) {
        entry.name = entry.name.trim().to_string();
        entry.url = entry.url.trim().to_string();
        if entry.name.is_empty() && entry.url.is_empty() {
            continue;
        }
        if entry.name.is_empty() || entry.url.is_empty() {
            anyhow::bail!("Each filter must include both a name and a URL.");
        }
        normalized.push(entry);
    }
    *entries = normalized;
    Ok(())
}

fn load_source_config(state: &AppState) -> Result<crate::native_runtime::SourceConfig> {
    let raw = fs::read_to_string(&state.config.source_config_file)
        .with_context(|| format!("read {}", state.config.source_config_file.display()))?;
    crate::native_runtime::parse_source_config_text(
        &raw,
        &state.config.source_config_file.display().to_string(),
    )
}

fn admin_credentials_match(state: &AppState, username: &str, password: &str) -> Result<bool> {
    let configured_password = fs::read_to_string(&state.config.admin_password_file)
        .with_context(|| format!("read {}", state.config.admin_password_file.display()))?;
    Ok(
        secure_string_eq(username.trim(), state.config.admin_username.trim())
            && secure_string_eq(password.trim_end_matches('\n'), configured_password.trim()),
    )
}

fn secure_string_eq(left: &str, right: &str) -> bool {
    let left = left.as_bytes();
    let right = right.as_bytes();
    if left.len() != right.len() {
        return false;
    }
    let mut diff = 0u8;
    for (l, r) in left.iter().zip(right.iter()) {
        diff |= l ^ r;
    }
    diff == 0
}

fn admin_session_token(headers: &HeaderMap) -> Option<String> {
    header_string(headers, &COOKIE).and_then(|raw| {
        raw.split(';').find_map(|entry| {
            let (name, value) = entry.trim().split_once('=')?;
            if name.trim() == ADMIN_SESSION_COOKIE {
                Some(value.trim().to_string())
            } else {
                None
            }
        })
    })
}

fn session_cookie_value(token: &str, max_age_seconds: u64, secure: bool) -> String {
    let secure_attr = if secure { "; Secure" } else { "" };
    format!(
        "{ADMIN_SESSION_COOKIE}={token}; Path=/; Max-Age={}; HttpOnly{secure_attr}; SameSite=Lax",
        max_age_seconds.max(1)
    )
}

fn expired_session_cookie_value(secure: bool) -> String {
    let secure_attr = if secure { "; Secure" } else { "" };
    format!("{ADMIN_SESSION_COOKIE}=; Path=/; Max-Age=0; HttpOnly{secure_attr}; SameSite=Lax")
}

fn identities_payload(state: &AppState) -> Result<Value> {
    let edge = state.db.edge_state(&state.config)?;
    let identities = state
        .db
        .list_identities()?
        .into_iter()
        .map(|identity| {
            let dot_hostname = identity
                .dot_label
                .as_ref()
                .map(|label| format!("{label}.{}", state.config.dot_hostname));
            let dot_target = dot_hostname.clone().unwrap_or_default();
            let is_expired = identity
                .expires_epoch_seconds
                .map(|value| value <= now_epoch_seconds())
                .unwrap_or(false);
            json!({
                "id": identity.id,
                "token": identity.token,
                "tokenMasked": mask_token(&identity.token),
                "dotLabel": identity.dot_label,
                "dotHostname": dot_hostname,
                "dotTarget": if dot_target.is_empty() { Value::Null } else { json!(dot_target.clone()) },
                "dotTargetMasked": if dot_target.is_empty() { Value::Null } else { json!(mask_dot_target(&dot_target, identity.dot_label.as_deref())) },
                "createdEpochSeconds": identity.created_epoch_seconds,
                "expiresEpochSeconds": identity.expires_epoch_seconds,
                "isExpired": is_expired,
            })
        })
        .collect::<Vec<_>>();
    Ok(json!({
        "primaryIdentityId": edge.primary_identity_id,
        "configuredPrimaryIdentityId": edge.configured_primary_identity_id,
        "dotIdentityEnabled": state.config.dot_identity_enabled,
        "dotHostnameBase": state.config.dot_hostname,
        "dotIdentityLabelLength": DOT_IDENTITY_LABEL_LENGTH,
        "identities": identities,
    }))
}

fn usage_payload(state: &AppState, identity: &str, window: &str) -> Result<Value> {
    let window_seconds = parse_duration_seconds(window, 7 * 24 * 60 * 60)?;
    let identity_filter = option_string(identity)
        .map(|value| normalize_identity_id(&value))
        .transpose()?;
    let payload = usage_from_observability(
        &state.config,
        &state.db,
        identity_filter.as_deref(),
        window_seconds,
    )?;
    Ok(payload)
}

fn querylog_summary_payload(state: &AppState, limit: usize, view_mode: &str) -> Result<Value> {
    let rows = state.db.querylog_mirror_latest_rows(limit)?;
    let include_internal = view_mode == "all";
    let (effective_rows, user_rows, internal_rows, internal_probe_counts) =
        split_querylog_rows(&rows, include_internal, true);
    let gateway_ip = std::env::var("ARBUZAS_DNS_LAN_GATEWAY_IP")
        .ok()
        .filter(|value| !value.trim().is_empty())
        .or_else(|| std::env::var("PIHOLE_REMOTE_ROUTER_LAN_IP").ok())
        .unwrap_or_default();
    let total_doh_count = count_querylog_doh(&effective_rows);
    let gateway_doh_count = count_client_doh(&effective_rows, &gateway_ip);
    let gateway_share_pct = if total_doh_count <= 0 {
        "0.00".to_string()
    } else {
        format!(
            "{:.2}",
            (gateway_doh_count as f64 * 100.0) / total_doh_count as f64
        )
    };
    let internal_probe_domain_counts = if internal_probe_counts.is_empty() {
        "none".to_string()
    } else {
        internal_probe_counts
            .into_iter()
            .collect::<Vec<_>>()
            .into_iter()
            .map(|(domain, count)| format!("{domain}:{count}"))
            .collect::<Vec<_>>()
            .join(";")
    };
    Ok(json!({
        "querylog_status": "ok",
        "querylog_view_mode": view_mode,
        "querylog_limit": limit,
        "include_internal_querylog": if include_internal { 1 } else { 0 },
        "internal_querylog_clients": internal_querylog_clients_csv(),
        "internal_probe_domains": internal_probe_domains_csv(),
        "total_query_count": effective_rows.len(),
        "total_doh_count": total_doh_count,
        "gateway_doh_count": gateway_doh_count,
        "gateway_share_pct": gateway_share_pct,
        "user_total_count": user_rows.len(),
        "user_doh_count": count_querylog_doh(&user_rows),
        "internal_total_count": internal_rows.len(),
        "internal_doh_count": count_querylog_doh(&internal_rows),
        "top_clients": top_clients(&effective_rows),
        "top_clients_user": top_clients(&user_rows),
        "top_clients_internal": top_clients(&internal_rows),
        "internal_probe_domain_counts": internal_probe_domain_counts,
    }))
}

fn querylog_proxy_payload(
    result: QuerylogMirrorQueryResult,
    identity_filter: &str,
    detail_level: QuerylogDetailLevel,
) -> Value {
    let rows = result
        .rows
        .iter()
        .map(|row| normalize_querylog_row(row, detail_level))
        .collect::<Vec<_>>();
    let data = rows.clone();
    json!({
        "rows": rows,
        "next_cursor": result.next_cursor,
        "has_more": result.has_more,
        "data": data,
        "oldest": result.next_cursor,
        "identityRequested": identity_filter,
        "matchCount": result.rows.len(),
        "meta": {
            "hasMore": result.has_more,
            "pagesScanned": 0,
            "unmatchedCount": result.unmatched_count,
            "mirrorSource": "native",
            "oldestTime": result
                .rows
                .last()
                .map(|row| row.row_time.clone())
                .unwrap_or_default(),
        },
    })
}

fn stats_payload_from_querylog(
    state: &AppState,
    query: &BTreeMap<String, Vec<String>>,
) -> Result<Value> {
    let window_seconds = match first_query_value(query, "interval")
        .trim()
        .to_ascii_lowercase()
        .as_str()
    {
        "7d" | "7_days" | "week" => 7 * 24 * 60 * 60,
        _ => 24 * 60 * 60,
    };
    let min_time_ms = (now_epoch_seconds() - window_seconds).max(0) * 1000;
    let start_hour_ms = min_time_ms - (min_time_ms % (60 * 60 * 1000));
    let conn = state.db.open_ro()?;
    let internal_clients = internal_querylog_clients();
    let mut sql = String::from(
        "SELECT client_ip, SUM(request_count) AS count FROM hourly_client_usage WHERE hour_start_ms >= ?",
    );
    let mut params_vec: Vec<Box<dyn rusqlite::ToSql>> = vec![Box::new(start_hour_ms)];
    if !internal_clients.is_empty() {
        let placeholders = std::iter::repeat_n("?", internal_clients.len())
            .collect::<Vec<_>>()
            .join(", ");
        sql.push_str(&format!(" AND client_ip NOT IN ({placeholders})"));
        for client in internal_clients {
            params_vec.push(Box::new(client));
        }
    }
    sql.push_str(" GROUP BY client_ip ORDER BY count DESC, client_ip ASC LIMIT 10");
    let mut statement = conn.prepare(&sql)?;
    let param_refs = params_vec
        .iter()
        .map(|value| value.as_ref() as &dyn rusqlite::ToSql)
        .collect::<Vec<_>>();
    let rows = statement.query_map(rusqlite::params_from_iter(param_refs), |row| {
        Ok((row.get::<_, String>(0)?, row.get::<_, i64>(1)?))
    })?;
    let top_clients = rows
        .filter_map(|row| row.ok())
        .map(|(client, count)| json!({ client: count }))
        .collect::<Vec<_>>();
    let total_queries = conn.query_row(
        "SELECT COALESCE(SUM(request_count), 0) FROM hourly_identity_usage WHERE hour_start_ms >= ?1",
        [start_hour_ms],
        |row| row.get::<_, i64>(0),
    )?;
    let total_blocked = conn.query_row(
        "SELECT COALESCE(SUM(request_count), 0) FROM hourly_querylog_status_usage WHERE hour_start_ms >= ?1 AND status_family = 'blocked'",
        [start_hour_ms],
        |row| row.get::<_, i64>(0),
    )?;
    Ok(json!({
        "interval": if window_seconds >= 7 * 24 * 60 * 60 { "7d" } else { "24h" },
        "dns_queries": total_queries,
        "blocked_filtering": total_blocked,
        "top_clients": top_clients,
    }))
}

fn clients_payload_from_querylog(state: &AppState, search: &str) -> Result<Value> {
    let conn = state.db.open_ro()?;
    let mut statement = conn.prepare(
        "SELECT client_ip, SUM(request_count) AS count
         FROM hourly_client_usage
         WHERE client_ip != ''
         GROUP BY client_ip
         ORDER BY count DESC, client_ip ASC
         LIMIT 100",
    )?;
    let rows = statement.query_map([], |row| {
        Ok((row.get::<_, String>(0)?, row.get::<_, i64>(1)?))
    })?;
    let search = search.trim().to_ascii_lowercase();
    let auto_clients = rows
        .filter_map(|row| row.ok())
        .map(|(client, count)| {
            let mut entry = minimal_client_entry(&client);
            set_object_value(&mut entry, "id", json!(client.clone()));
            set_object_value(&mut entry, "ip", json!(client.clone()));
            set_object_value(&mut entry, "ids", json!([client.clone()]));
            set_object_value(&mut entry, "name", json!(client.clone()));
            set_object_value(&mut entry, "query_count", json!(count));
            if !has_nonempty_object(entry.get("whois_info")) {
                set_object_value(&mut entry, "whois_info", cached_whois_info(&client));
            }
            entry
        })
        .filter(|entry| client_entry_matches_search(entry, &search))
        .collect::<Vec<_>>();
    Ok(json!({
        "auto_clients": auto_clients,
        "clients": [],
    }))
}

fn client_entry_matches_search(entry: &Value, search: &str) -> bool {
    if search.is_empty() {
        return true;
    }
    let matches = |value: Option<&str>| {
        value
            .map(|value| value.to_ascii_lowercase().contains(search))
            .unwrap_or(false)
    };
    if matches(entry.get("ip").and_then(Value::as_str))
        || matches(entry.get("id").and_then(Value::as_str))
        || matches(entry.get("name").and_then(Value::as_str))
    {
        return true;
    }
    if entry
        .get("ids")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(Value::as_str)
        .any(|value| value.to_ascii_lowercase().contains(search))
    {
        return true;
    }
    let whois = entry.get("whois_info").and_then(Value::as_object);
    matches(
        whois
            .and_then(|value| value.get("country"))
            .and_then(Value::as_str),
    ) || matches(
        whois
            .and_then(|value| value.get("orgname"))
            .and_then(Value::as_str),
    ) || matches(
        whois
            .and_then(|value| value.get("org"))
            .and_then(Value::as_str),
    )
}

async fn require_session(
    state: &AppState,
    headers: &HeaderMap,
    is_api: bool,
) -> Result<Option<Response>> {
    if state.config.skip_session_check {
        return Ok(None);
    }
    if session_authenticated(state, headers).await? {
        return Ok(None);
    }
    Ok(Some(if is_api {
        json_response(StatusCode::UNAUTHORIZED, json!({ "error": "Unauthorized" }))
    } else {
        redirect_response("/login".to_string())
    }))
}

async fn session_authenticated(state: &AppState, headers: &HeaderMap) -> Result<bool> {
    if state.config.skip_session_check {
        return Ok(true);
    }
    let Some(token) = admin_session_token(headers) else {
        return Ok(false);
    };
    state
        .write_serialized(|db| {
            db.touch_admin_session(&token, state.config.admin_session_ttl_seconds)
        })
        .await
}

fn requires_same_origin(path: &str) -> bool {
    path == "/dns/api/session"
        || path == "/dns/api/identities"
        || path == "/dns/api/settings/validate"
        || path == "/dns/api/settings/apply"
        || (path.starts_with("/dns/api/identities/")
            && (path.ends_with("/rename")
                || !path.ends_with("/apple-doh.mobileconfig")
                    && !path.ends_with("/apple-dot.mobileconfig")))
}

fn same_origin_valid(headers: &HeaderMap, request_is_secure: bool) -> bool {
    let Some(host) = headers
        .get("host")
        .and_then(|value| value.to_str().ok())
        .map(|value| {
            value
                .split(',')
                .next()
                .unwrap_or_default()
                .trim()
                .to_string()
        })
        .filter(|value| !value.is_empty())
    else {
        return false;
    };
    let forwarded_proto = headers
        .get("x-forwarded-proto")
        .and_then(|value| value.to_str().ok())
        .map(|value| {
            value
                .split(',')
                .next()
                .unwrap_or_default()
                .trim()
                .to_string()
        })
        .filter(|value| !value.is_empty());
    let mut expected = BTreeSet::new();
    if let Some(proto) = forwarded_proto {
        expected.insert(format!("{proto}://{host}"));
    } else {
        expected.insert(format!(
            "{}://{host}",
            if request_is_secure { "https" } else { "http" }
        ));
        expected.insert(format!("https://{host}"));
    }
    let origin = normalize_origin(
        headers
            .get("origin")
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default(),
    );
    let referer = normalize_origin(
        headers
            .get("referer")
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default(),
    );
    if !origin.is_empty() {
        return expected.contains(&origin);
    }
    if !referer.is_empty() {
        return expected.contains(&referer);
    }
    false
}

fn normalize_origin(raw: &str) -> String {
    if raw.trim().is_empty() {
        return String::new();
    }
    Url::parse(raw)
        .ok()
        .and_then(|url| {
            url.host_str().map(|host| {
                format!(
                    "{}://{}",
                    url.scheme(),
                    authority_from_url_host(host, url.port())
                )
            })
        })
        .unwrap_or_default()
}

fn normalize_return_target(raw: String) -> String {
    let trimmed = raw.trim();
    if trimmed.is_empty()
        || trimmed == "/"
        || trimmed == "/index.html"
        || trimmed == "/login"
        || trimmed == "/login.html"
    {
        return LOGIN_RETURN_DEFAULT.to_string();
    }
    if trimmed.starts_with("/#") {
        let hash = trimmed.trim_start_matches('/');
        if hash == "#settings" || hash == "#dns" {
            return format!("{ADMIN_BASE_PATH}/settings");
        }
        if hash == "#filters" {
            return format!("{ADMIN_BASE_PATH}/settings#filters");
        }
        if hash == "#encryption" {
            return format!("{ADMIN_BASE_PATH}/settings#encryption");
        }
        if hash == "#clients" {
            return format!("{ADMIN_BASE_PATH}/clients");
        }
        if hash == "#dhcp" {
            return format!("{ADMIN_BASE_PATH}/settings#dhcp");
        }
        return LOGIN_RETURN_DEFAULT.to_string();
    }
    if trimmed.starts_with('/') && !trimmed.starts_with("//") {
        return trimmed.to_string();
    }
    LOGIN_RETURN_DEFAULT.to_string()
}

fn render_admin_asset(template: &str, active_item: AdminNavItem) -> String {
    template.replace(ADMIN_NAV_PLACEHOLDER, &render_admin_nav(active_item))
}

fn render_identity_page(
    template: &str,
    active_item: AdminNavItem,
    default_view: &str,
    updated_at: &str,
) -> String {
    render_admin_asset(template, active_item)
        .replace("__QUERYLOG_VIEW__", default_view)
        .replace("__QUERYLOG_UPDATED_AT__", updated_at)
}

fn render_admin_nav(active_item: AdminNavItem) -> String {
    let items = [
        (AdminNavItem::Overview, ADMIN_BASE_PATH.to_string(), "Overview"),
        (
            AdminNavItem::Settings,
            format!("{ADMIN_BASE_PATH}/settings"),
            "Settings",
        ),
        (
            AdminNavItem::Clients,
            format!("{ADMIN_BASE_PATH}/clients"),
            "Clients",
        ),
        (
            AdminNavItem::Identities,
            format!("{ADMIN_BASE_PATH}/identities"),
            "Identities",
        ),
        (
            AdminNavItem::Queries,
            format!("{ADMIN_BASE_PATH}/queries"),
            "Queries",
        ),
    ];
    let mut nav = String::new();
    for (item, href, label) in items {
        if item == active_item {
            nav.push_str(&format!("<a class=\"active\" href=\"{href}\">{label}</a>"));
        } else {
            nav.push_str(&format!("<a href=\"{href}\">{label}</a>"));
        }
    }
    nav.push_str("<button id=\"logout\" type=\"button\">Log out</button>");
    nav
}

fn parse_query(raw: &str) -> BTreeMap<String, Vec<String>> {
    let mut out = BTreeMap::<String, Vec<String>>::new();
    for (key, value) in form_urlencoded::parse(raw.as_bytes()) {
        out.entry(key.into_owned())
            .or_default()
            .push(value.into_owned());
    }
    out
}

fn first_query_value(query: &BTreeMap<String, Vec<String>>, key: &str) -> String {
    query
        .get(key)
        .and_then(|values| values.first())
        .cloned()
        .unwrap_or_default()
}

fn parse_json_object(body: &Bytes) -> Result<Map<String, Value>> {
    let payload = serde_json::from_slice::<Value>(body).context("parse JSON body")?;
    payload
        .as_object()
        .cloned()
        .ok_or_else(|| anyhow!("JSON body must be an object."))
}

fn parse_optional_epoch(value: Option<&Value>) -> Result<Option<i64>> {
    match value {
        None | Some(Value::Null) => Ok(None),
        Some(Value::Number(number)) => Ok(number.as_i64().filter(|value| *value > 0)),
        Some(Value::String(text)) => {
            let trimmed = text.trim();
            if trimmed.is_empty() {
                Ok(None)
            } else {
                Ok(Some(
                    trimmed
                        .parse::<i64>()
                        .context("epoch must be an integer or null")?,
                ))
            }
        }
        _ => Err(anyhow!("epoch must be an integer or null")),
    }
}

fn parse_querylog_limit(raw: String) -> Result<usize> {
    let value = if raw.trim().is_empty() {
        QUERYLOG_LIMIT_DEFAULT
    } else {
        raw.trim()
            .parse::<usize>()
            .with_context(|| "Invalid querylog limit. Expected integer 1-10000.")?
    };
    if !(QUERYLOG_LIMIT_MIN..=QUERYLOG_LIMIT_MAX).contains(&value) {
        anyhow::bail!("Invalid querylog limit. Expected integer 1-10000.");
    }
    Ok(value)
}

fn parse_querylog_page_size(raw: String) -> Result<usize> {
    let value = if raw.trim().is_empty() {
        QUERYLOG_PAGE_SIZE_DEFAULT
    } else {
        raw.trim()
            .parse::<usize>()
            .with_context(|| "Invalid querylog page size. Expected a positive integer.")?
    };
    if value == 0 {
        anyhow::bail!("Invalid querylog page size. Expected a positive integer.");
    }
    Ok(value)
}

fn parse_querylog_view(raw: String) -> Result<String> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "" | "user_only" => Ok("user_only".to_string()),
        "all" => Ok("all".to_string()),
        _ => anyhow::bail!("Invalid querylog view. Expected user_only or all."),
    }
}

fn normalize_querylog_response_status(raw: &str) -> String {
    match raw.trim().to_ascii_lowercase().as_str() {
        "filtered"
        | "blocked"
        | "blocked_services"
        | "blocked_safebrowsing"
        | "blocked_parental"
        | "whitelisted"
        | "rewritten"
        | "safe_search"
        | "processed" => raw.trim().to_ascii_lowercase(),
        _ => "all".to_string(),
    }
}

fn split_querylog_rows(
    rows: &[QuerylogMirrorRow],
    include_internal: bool,
    use_original_client_for_internal: bool,
) -> (
    Vec<QuerylogMirrorRow>,
    Vec<QuerylogMirrorRow>,
    Vec<QuerylogMirrorRow>,
    BTreeMap<String, i64>,
) {
    let internal_clients = internal_querylog_clients();
    let internal_probe_domains = internal_probe_domains();
    let mut user_rows = Vec::new();
    let mut internal_rows = Vec::new();
    let mut internal_probe_counts = BTreeMap::<String, i64>::new();
    for row in rows.iter().cloned() {
        let client = if use_original_client_for_internal {
            querylog_internal_client_label(&row)
        } else {
            querylog_client_label(&row)
        };
        let is_internal = internal_clients.contains(&client);
        if is_internal {
            internal_rows.push(row.clone());
            let qname = querylog_qname(&row);
            if internal_probe_domains.contains(&qname) {
                *internal_probe_counts.entry(qname).or_insert(0) += 1;
            }
        } else {
            user_rows.push(row.clone());
        }
    }
    let effective_rows = if include_internal {
        rows.to_vec()
    } else {
        user_rows.clone()
    };
    (
        effective_rows,
        user_rows,
        internal_rows,
        internal_probe_counts,
    )
}

fn querylog_qname(row: &QuerylogMirrorRow) -> String {
    row.payload
        .get("question")
        .and_then(Value::as_object)
        .and_then(|question| question.get("name"))
        .and_then(Value::as_str)
        .unwrap_or(&row.query_name)
        .trim()
        .to_ascii_lowercase()
}

fn querylog_client_label(row: &QuerylogMirrorRow) -> String {
    row.payload
        .get("client")
        .and_then(Value::as_str)
        .filter(|value| !value.trim().is_empty())
        .unwrap_or(&row.client)
        .trim()
        .to_string()
}

fn querylog_internal_client_label(row: &QuerylogMirrorRow) -> String {
    row.payload
        .get("originalClient")
        .and_then(Value::as_str)
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| {
            if !row.original_client.trim().is_empty() {
                &row.original_client
            } else {
                row.payload
                    .get("client")
                    .and_then(Value::as_str)
                    .filter(|value| !value.trim().is_empty())
                    .unwrap_or(&row.client)
            }
        })
        .trim()
        .to_string()
}

fn count_querylog_doh(rows: &[QuerylogMirrorRow]) -> usize {
    rows.iter().filter(|row| row.protocol == "doh").count()
}

fn count_client_doh(rows: &[QuerylogMirrorRow], client_ip: &str) -> usize {
    if client_ip.trim().is_empty() {
        return 0;
    }
    rows.iter()
        .filter(|row| row.protocol == "doh" && querylog_client_label(row) == client_ip)
        .count()
}

fn top_clients(rows: &[QuerylogMirrorRow]) -> String {
    if rows.is_empty() {
        return "none".to_string();
    }
    let mut counts = BTreeMap::<(String, String), i64>::new();
    for row in rows {
        let client = querylog_client_label(row);
        let protocol = row.protocol.clone();
        if client.is_empty() {
            continue;
        }
        *counts.entry((client, protocol)).or_insert(0) += 1;
    }
    if counts.is_empty() {
        return "none".to_string();
    }
    let mut ranked = counts.into_iter().collect::<Vec<_>>();
    ranked.sort_by(|left, right| {
        right
            .1
            .cmp(&left.1)
            .then_with(|| left.0 .0.cmp(&right.0 .0))
            .then_with(|| left.0 .1.cmp(&right.0 .1))
    });
    ranked
        .into_iter()
        .take(10)
        .map(|((client, protocol), count)| format!("{client}:{protocol}:{count}"))
        .collect::<Vec<_>>()
        .join(";")
}

fn normalize_querylog_row(row: &QuerylogMirrorRow, detail_level: QuerylogDetailLevel) -> Value {
    let mut payload = row.payload.clone();
    set_object_value(&mut payload, "rowFingerprint", json!(row.row_fingerprint.clone()));
    set_object_value(&mut payload, "detailMode", json!(detail_level.as_str()));
    if !row.identity_id.is_empty() {
        set_object_value(&mut payload, "identityId", json!(row.identity_id.clone()));
        let mut identity = payload
            .get("identity")
            .and_then(Value::as_object)
            .cloned()
            .unwrap_or_default();
        identity.insert("id".to_string(), json!(row.identity_id.clone()));
        identity.insert("label".to_string(), json!(identity_label(&row.identity_id)));
        set_object_value(&mut payload, "identity", Value::Object(identity));
    }
    if payload.get("time").is_none() {
        set_object_value(&mut payload, "time", json!(row.row_time.clone()));
    }
    if payload.get("status").is_none() {
        set_object_value(&mut payload, "status", json!(fallback_response_status(row)));
    }
    if payload.get("originalClient").is_none() && !row.original_client.trim().is_empty() {
        set_object_value(
            &mut payload,
            "originalClient",
            json!(row.original_client.clone()),
        );
    }
    if payload.get("display").is_none() {
        set_object_value(&mut payload, "display", fallback_querylog_display(row));
    } else if let Some(display) = payload.get_mut("display").and_then(Value::as_object_mut) {
        display.insert(
            "identityLabel".to_string(),
            json!(identity_label(&row.identity_id)),
        );
    }
    payload
}

fn fallback_querylog_display(row: &QuerylogMirrorRow) -> Value {
    let (status_tone, status_label, response_status) = fallback_status_display(row);
    let summary = if !row.block_category.trim().is_empty() {
        match row.block_category.trim() {
            "blocked_safebrowsing" => "Blocked by safe browsing".to_string(),
            "blocked_parental" => "Blocked by parental protection".to_string(),
            "blocked_services" => "Blocked service".to_string(),
            _ => "Blocked by filters".to_string(),
        }
    } else if row.status_raw.trim().is_empty() {
        "No details yet".to_string()
    } else {
        response_status.clone()
    };
    let display_block_category = match row.block_category.trim() {
        "blocked_safebrowsing" => "safebrowsing",
        "blocked_parental" => "adult",
        value if !value.is_empty() => "filtered",
        _ => "",
    };
    json!({
        "statusTone": status_tone,
        "statusLabel": status_label,
        "responseStatus": response_status,
        "blockCategory": display_block_category,
        "summary": summary,
        "protocolLabel": row.protocol.to_ascii_uppercase(),
        "queryName": row.query_name,
        "queryType": row.query_type,
        "clientLabel": row.client,
        "originalClientLabel": row.original_client,
        "identityLabel": identity_label(&row.identity_id),
        "whoisOrg": "",
        "details": [],
    })
}

fn fallback_status_display(row: &QuerylogMirrorRow) -> (String, String, String) {
    let response_status = fallback_response_status(row);
    if row.block_category == "blocked_safebrowsing" {
        return (
            "blocked".to_string(),
            "Blocked malware/phishing".to_string(),
            response_status,
        );
    }
    if row.block_category == "blocked_parental" {
        return (
            "blocked".to_string(),
            "Blocked adult content".to_string(),
            response_status,
        );
    }
    if row.block_category == "blocked_services" {
        return (
            "blocked".to_string(),
            "Blocked service".to_string(),
            response_status,
        );
    }
    if !row.block_category.trim().is_empty() {
        return (
            "blocked".to_string(),
            "Blocked by filters".to_string(),
            response_status,
        );
    }
    match row.status_raw.trim().to_ascii_uppercase().as_str() {
        "NOERROR" | "PROCESSED" => (
            "allowed".to_string(),
            "Allowed".to_string(),
            response_status,
        ),
        "NXDOMAIN" | "SERVFAIL" | "REFUSED" => (
            "warn".to_string(),
            row.status_raw.trim().to_ascii_uppercase(),
            response_status,
        ),
        _ => (
            "neutral".to_string(),
            if response_status.is_empty() {
                "Unknown".to_string()
            } else {
                response_status.clone()
            },
            response_status,
        ),
    }
}

fn fallback_response_status(row: &QuerylogMirrorRow) -> String {
    if !row.block_category.trim().is_empty() {
        row.block_category.clone()
    } else if row.status_raw.trim().is_empty() {
        "UNKNOWN".to_string()
    } else {
        row.status_raw.trim().to_ascii_uppercase()
    }
}

fn parse_querylog_detail_level(raw: &str) -> Result<QuerylogDetailLevel> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "" | "summary" => Ok(QuerylogDetailLevel::Summary),
        "full" => Ok(QuerylogDetailLevel::Full),
        other => Err(anyhow!(
            "Invalid detail value '{other}'. Expected summary or full."
        )),
    }
}

fn identity_label(identity_id: &str) -> String {
    match identity_id.trim() {
        "__bare__" => "Bare path".to_string(),
        "__unknown__" => "Unknown token".to_string(),
        value if !value.is_empty() => value.to_string(),
        _ => "Unknown identity".to_string(),
    }
}

fn internal_querylog_clients() -> BTreeSet<String> {
    normalize_csv_env(
        "ARBUZAS_DNS_INTERNAL_QUERYLOG_CLIENTS",
        INTERNAL_QUERYLOG_CLIENTS_DEFAULT,
    )
}

fn internal_querylog_clients_csv() -> String {
    normalize_csv_env(
        "ARBUZAS_DNS_INTERNAL_QUERYLOG_CLIENTS",
        INTERNAL_QUERYLOG_CLIENTS_DEFAULT,
    )
    .into_iter()
    .collect::<Vec<_>>()
    .join(",")
}

fn internal_probe_domains() -> BTreeSet<String> {
    normalize_csv_env(
        "ARBUZAS_DNS_INTERNAL_PROBE_DOMAINS",
        INTERNAL_PROBE_DOMAINS_DEFAULT,
    )
}

fn internal_probe_domains_csv() -> String {
    normalize_csv_env(
        "ARBUZAS_DNS_INTERNAL_PROBE_DOMAINS",
        INTERNAL_PROBE_DOMAINS_DEFAULT,
    )
    .into_iter()
    .collect::<Vec<_>>()
    .join(",")
}

fn normalize_csv_env(name: &str, default: &str) -> BTreeSet<String> {
    std::env::var(name)
        .unwrap_or_else(|_| default.to_string())
        .split(',')
        .map(|value| value.trim().to_ascii_lowercase())
        .filter(|value| !value.is_empty())
        .collect()
}

fn find_identity_entry(payload: &Value, identity_id: &str) -> Option<Value> {
    payload
        .get("identities")
        .and_then(Value::as_array)
        .and_then(|identities| {
            identities
                .iter()
                .find(|entry| {
                    entry
                        .get("id")
                        .and_then(Value::as_str)
                        .map(|value| value == identity_id)
                        .unwrap_or(false)
                })
                .cloned()
        })
}

fn build_apple_doh_profile(entry: &Value, headers: &HeaderMap) -> Result<(String, String)> {
    if entry
        .get("isExpired")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        anyhow::bail!("Expired identities cannot generate Apple DNS profiles.");
    }
    let identity_id = entry.get("id").and_then(Value::as_str).unwrap_or_default();
    let token = entry
        .get("token")
        .and_then(Value::as_str)
        .unwrap_or_default();
    if token.is_empty() {
        anyhow::bail!("Identity is missing a DoH token.");
    }
    let doh_url = public_doh_url_for_token(headers, token)?;
    let identifier_component = sanitize_identifier_component(identity_id);
    let identifier_base = format!("lv.jolkins.arbuzas.dnsidentity.{identifier_component}");
    let payload = configuration_plist(
        &identifier_base,
        &deterministic_uuid(&format!("profile:{identifier_base}:{doh_url}")),
        &format!("DNS identity {identity_id}"),
        &format!("Installs the encrypted DNS profile for {identity_id}."),
        &format!("{identifier_base}.doh"),
        &deterministic_uuid(&format!("doh:{identifier_base}:{doh_url}")),
        &format!("DNS identity {identity_id}"),
        &format!(
            "<key>DNSProtocol</key><string>HTTPS</string><key>ServerURL</key><string>{}</string><key>MatchingDomains</key><array><string></string></array>",
            escape_xml(&doh_url)
        ),
    );
    Ok((payload, format!("{identity_id}-apple-doh.mobileconfig")))
}

fn build_apple_dot_profile(entry: &Value, headers: &HeaderMap) -> Result<(String, String)> {
    if entry
        .get("isExpired")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        anyhow::bail!("Expired identities cannot generate Apple DNS profiles.");
    }
    let identity_id = entry.get("id").and_then(Value::as_str).unwrap_or_default();
    let server_name = entry
        .get("dotHostname")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    if server_name.is_empty() {
        anyhow::bail!("Identity is missing a DoT hostname.");
    }
    let server_addresses = public_server_addresses(headers)?;
    if server_addresses.is_empty() {
        anyhow::bail!("Could not determine the public DoT server addresses for this request.");
    }
    let identifier_component = sanitize_identifier_component(identity_id);
    let identifier_base = format!("lv.jolkins.arbuzas.dnsidentity.{identifier_component}");
    let addresses_xml = server_addresses
        .iter()
        .map(|value| format!("<string>{}</string>", escape_xml(value)))
        .collect::<Vec<_>>()
        .join("");
    let payload = configuration_plist(
        &identifier_base,
        &deterministic_uuid(&format!(
            "profile-dot:{identifier_base}:{server_name}:{}",
            server_addresses.join(",")
        )),
        &format!("DNS identity {identity_id} (TLS)"),
        &format!("Installs the encrypted DNS-over-TLS profile for {identity_id}."),
        &format!("{identifier_base}.dot"),
        &deterministic_uuid(&format!(
            "dot:{identifier_base}:{server_name}:{}",
            server_addresses.join(",")
        )),
        &format!("DNS identity {identity_id} (TLS)"),
        &format!(
            "<key>DNSProtocol</key><string>TLS</string><key>ServerName</key><string>{}</string><key>ServerAddresses</key><array>{}</array><key>MatchingDomains</key><array><string></string></array>",
            escape_xml(&server_name),
            addresses_xml
        ),
    );
    Ok((payload, format!("{identity_id}-apple-dot.mobileconfig")))
}

fn configuration_plist(
    identifier_base: &str,
    profile_uuid: &str,
    display_name: &str,
    description: &str,
    content_identifier: &str,
    content_uuid: &str,
    content_display_name: &str,
    dns_settings_xml: &str,
) -> String {
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadType</key><string>Configuration</string>
  <key>PayloadVersion</key><integer>1</integer>
  <key>PayloadIdentifier</key><string>{}</string>
  <key>PayloadUUID</key><string>{}</string>
  <key>PayloadDisplayName</key><string>{}</string>
  <key>PayloadDescription</key><string>{}</string>
  <key>PayloadOrganization</key><string>Jolkins</string>
  <key>PayloadRemovalDisallowed</key><false/>
  <key>PayloadScope</key><string>System</string>
  <key>PayloadContent</key>
  <array>
    <dict>
      <key>PayloadType</key><string>com.apple.dnsSettings.managed</string>
      <key>PayloadVersion</key><integer>1</integer>
      <key>PayloadIdentifier</key><string>{}</string>
      <key>PayloadUUID</key><string>{}</string>
      <key>PayloadDisplayName</key><string>{}</string>
      <key>DNSSettings</key>
      <dict>{}</dict>
    </dict>
  </array>
</dict>
</plist>
"#,
        escape_xml(identifier_base),
        escape_xml(profile_uuid),
        escape_xml(display_name),
        escape_xml(description),
        escape_xml(content_identifier),
        escape_xml(content_uuid),
        escape_xml(content_display_name),
        dns_settings_xml,
    )
}

fn public_doh_url_for_token(headers: &HeaderMap, token: &str) -> Result<String> {
    let host = request_public_hostname(headers)?;
    let authority = format_https_authority(&host, 443);
    Ok(format!(
        "https://{}/{}/dns-query",
        authority,
        utf8_percent_encode(token, NON_ALPHANUMERIC)
    ))
}

fn public_server_addresses(headers: &HeaderMap) -> Result<Vec<String>> {
    let host = request_public_hostname(headers)?;
    if host.parse::<IpAddr>().is_ok() {
        return Ok(vec![host]);
    }
    let mut seen = BTreeSet::new();
    let mut out = Vec::new();
    for address in (host.as_str(), 0u16).to_socket_addrs()? {
        let ip = address.ip().to_string();
        if seen.insert(ip.clone()) {
            out.push(ip);
        }
    }
    Ok(out)
}

fn request_public_hostname(headers: &HeaderMap) -> Result<String> {
    let raw_host = headers
        .get("host")
        .and_then(|value| value.to_str().ok())
        .map(|value| {
            value
                .split(',')
                .next()
                .unwrap_or_default()
                .trim()
                .to_string()
        })
        .filter(|value| !value.is_empty())
        .ok_or_else(|| anyhow!("Could not determine the public host for this request."))?;
    let parsed = Url::parse(&format!("https://{raw_host}"))?;
    Ok(parsed.host_str().unwrap_or(&raw_host).to_string())
}

fn sanitize_identifier_component(identity_id: &str) -> String {
    let sanitized = identity_id
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || matches!(ch, '.' | '-') {
                ch
            } else {
                '-'
            }
        })
        .collect::<String>()
        .trim_matches(|ch| ch == '.' || ch == '-')
        .to_string();
    if sanitized.is_empty() {
        "identity".to_string()
    } else {
        sanitized
    }
}

fn deterministic_uuid(seed: &str) -> String {
    let digest = Sha256::digest(seed.as_bytes());
    let mut bytes = [0u8; 16];
    bytes.copy_from_slice(&digest[..16]);
    bytes[6] = (bytes[6] & 0x0f) | 0x50;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    format!(
        "{:08x}-{:04x}-{:04x}-{:04x}-{:012x}",
        u32::from_be_bytes([bytes[0], bytes[1], bytes[2], bytes[3]]),
        u16::from_be_bytes([bytes[4], bytes[5]]),
        u16::from_be_bytes([bytes[6], bytes[7]]),
        u16::from_be_bytes([bytes[8], bytes[9]]),
        u64::from_be_bytes([
            0, 0, bytes[10], bytes[11], bytes[12], bytes[13], bytes[14], bytes[15]
        ])
    )
}

fn escape_xml(raw: &str) -> String {
    raw.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn format_https_authority(hostname: &str, port: u16) -> String {
    if hostname.contains(':') && !hostname.starts_with('[') {
        if port == 443 {
            format!("[{hostname}]")
        } else {
            format!("[{hostname}]:{port}")
        }
    } else if port == 443 {
        hostname.to_string()
    } else {
        format!("{hostname}:{port}")
    }
}

fn authority_from_url_host(host: &str, port: Option<u16>) -> String {
    match port {
        Some(port) => format_https_authority(host, port),
        None => {
            if host.contains(':') && !host.starts_with('[') {
                format!("[{host}]")
            } else {
                host.to_string()
            }
        }
    }
}

fn minimal_client_entry(client_id: &str) -> Value {
    json!({
        "disallowed": false,
        "whois_info": {},
        "safe_search": null,
        "blocked_services_schedule": null,
        "name": "",
        "blocked_services": null,
        "ids": [client_id],
        "tags": null,
        "upstreams": null,
        "filtering_enabled": false,
        "parental_enabled": false,
        "safebrowsing_enabled": false,
        "safesearch_enabled": false,
        "use_global_blocked_services": false,
        "use_global_settings": false,
        "ignore_querylog": null,
        "ignore_statistics": null,
        "upstreams_cache_size": 0,
        "upstreams_cache_enabled": null,
    })
}

fn decode_path_segment(raw: &str) -> Result<String> {
    Ok(percent_decode_str(raw)
        .decode_utf8()
        .context("decode path segment")?
        .trim()
        .to_ascii_lowercase())
}

fn option_string(raw: &str) -> Option<String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || trimmed == "all" {
        None
    } else {
        Some(trimmed.to_string())
    }
}

fn mask_token(token: &str) -> String {
    match token.len() {
        0..=4 => "*".repeat(token.len()),
        5..=10 => format!(
            "{}{}{}",
            &token[..2],
            "*".repeat(token.len() - 4),
            &token[token.len() - 2..]
        ),
        _ => format!("{}...{}", &token[..4], &token[token.len() - 4..]),
    }
}

fn mask_dot_target(hostname: &str, dot_label: Option<&str>) -> String {
    if hostname.is_empty() {
        return String::new();
    }
    let label = dot_label.unwrap_or_default().trim().to_ascii_lowercase();
    let normalized_host = hostname.to_ascii_lowercase();
    if !label.is_empty() && normalized_host.starts_with(&format!("{label}.")) {
        return format!("{}{}", mask_token(&label), &hostname[label.len()..]);
    }
    match hostname.split_once('.') {
        Some((first, rest)) => format!("{}.{}", mask_token(first), rest),
        None => mask_token(hostname),
    }
}

fn has_nonempty_object(value: Option<&Value>) -> bool {
    value
        .and_then(Value::as_object)
        .map(|value| !value.is_empty())
        .unwrap_or(false)
}

fn set_object_value(target: &mut Value, key: &str, value: Value) {
    if let Some(object) = target.as_object_mut() {
        object.insert(key.to_string(), value);
    }
}

fn header_string(headers: &HeaderMap, name: &HeaderName) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(ToString::to_string)
}

fn json_response(status: StatusCode, payload: Value) -> Response {
    let body = serde_json::to_vec(&payload).unwrap_or_else(|_| b"{}".to_vec());
    bytes_response(status, "application/json; charset=utf-8", body, &[])
}

fn json_response_with_headers(
    status: StatusCode,
    payload: Value,
    extra_headers: &[(&str, String)],
) -> Response {
    let body = serde_json::to_vec(&payload).unwrap_or_else(|_| b"{}".to_vec());
    bytes_response(
        status,
        "application/json; charset=utf-8",
        body,
        extra_headers,
    )
}

fn text_response(status: StatusCode, content_type: &str, payload: String) -> Response {
    bytes_response(status, content_type, payload.into_bytes(), &[])
}

fn bytes_response(
    status: StatusCode,
    content_type: &str,
    payload: Vec<u8>,
    extra_headers: &[(&str, String)],
) -> Response {
    let mut builder = Response::builder()
        .status(status)
        .header("Content-Type", content_type)
        .header("Cache-Control", "no-store")
        .header("Content-Length", payload.len().to_string());
    for (key, value) in extra_headers {
        builder = builder.header(*key, value);
    }
    builder
        .body(Body::from(payload))
        .unwrap_or_else(|_| (StatusCode::INTERNAL_SERVER_ERROR, "internal error").into_response())
}

fn redirect_response(location: String) -> Response {
    Response::builder()
        .status(StatusCode::FOUND)
        .header("Location", location)
        .header("Cache-Control", "no-store")
        .body(Body::empty())
        .unwrap_or_else(|_| (StatusCode::INTERNAL_SERVER_ERROR, "internal error").into_response())
}

fn method_not_allowed() -> Response {
    json_response(
        StatusCode::METHOD_NOT_ALLOWED,
        json!({ "error": "Method not allowed" }),
    )
}
