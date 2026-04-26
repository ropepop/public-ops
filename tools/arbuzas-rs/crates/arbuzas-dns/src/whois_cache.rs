use std::fs;
use std::path::Path;
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant, SystemTime};

use serde_json::{json, Value};

const WHOIS_CACHE_CHECK_INTERVAL: Duration = Duration::from_secs(5);

#[derive(Debug, Default)]
struct WhoisCacheState {
    path: String,
    last_checked: Option<Instant>,
    modified: Option<SystemTime>,
    payload: Value,
}

fn whois_cache_state() -> &'static Mutex<WhoisCacheState> {
    static STATE: OnceLock<Mutex<WhoisCacheState>> = OnceLock::new();
    STATE.get_or_init(|| Mutex::new(WhoisCacheState::default()))
}

fn load_whois_payload(path: &str) -> (Option<SystemTime>, Value) {
    let file_path = Path::new(path);
    let modified = file_path.metadata().ok().and_then(|metadata| metadata.modified().ok());
    let payload = fs::read_to_string(file_path)
        .ok()
        .and_then(|raw| serde_json::from_str::<Value>(&raw).ok())
        .unwrap_or_else(|| json!({}));
    (modified, payload)
}

pub(crate) fn cached_whois_info(ip: &str) -> Value {
    if ip.trim().is_empty() {
        return json!({});
    }
    let Some(path) = std::env::var("ARBUZAS_DNS_IPINFO_CACHE_FILE")
        .ok()
        .filter(|value| !value.trim().is_empty())
    else {
        return json!({});
    };

    let mut state = whois_cache_state()
        .lock()
        .expect("whois cache mutex poisoned");
    let now = Instant::now();
    let should_refresh = state.path != path
        || state
            .last_checked
            .map(|checked_at| now.duration_since(checked_at) >= WHOIS_CACHE_CHECK_INTERVAL)
            .unwrap_or(true);

    if should_refresh {
        let modified = Path::new(&path)
            .metadata()
            .ok()
            .and_then(|metadata| metadata.modified().ok());
        let path_changed = state.path != path;
        let file_changed = modified != state.modified;
        if path_changed || file_changed {
            let (loaded_modified, payload) = load_whois_payload(&path);
            state.payload = payload;
            state.modified = loaded_modified;
        } else if !Path::new(&path).is_file() {
            state.payload = json!({});
            state.modified = None;
        }
        state.path = path;
        state.last_checked = Some(now);
    }

    state
        .payload
        .get(ip)
        .and_then(Value::as_object)
        .and_then(|entry| entry.get("whois_info"))
        .cloned()
        .unwrap_or_else(|| json!({}))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn returns_empty_when_cache_file_missing() {
        std::env::set_var(
            "ARBUZAS_DNS_IPINFO_CACHE_FILE",
            "/tmp/arbuzas-whois-cache-missing.json",
        );
        let payload = cached_whois_info("1.1.1.1");
        assert_eq!(payload, json!({}));
    }
}
