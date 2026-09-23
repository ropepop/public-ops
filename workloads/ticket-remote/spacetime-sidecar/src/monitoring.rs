//! Private reads use the existing subscribed cache; events never poll Maincloud.
use super::*;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct PushSubscriptionStatusRequest {
    ticket_id: String,
    email: String,
    endpoint: String,
}

pub(super) fn admin_role(conn: &DbConnection, ticket: &str, email: &str) -> Option<String> {
    let email = email.trim().to_ascii_lowercase();
    conn.db
        .ticketremote_service_ticket_member()
        .iter()
        .find(|m| {
            m.ticket_id == ticket
                && m.email == email
                && m.active
                && matches!(m.role.as_str(), "owner" | "admin")
        })
        .map(|m| m.role)
}

pub(super) fn read(
    app: &Arc<AppState>,
    request: &HttpRequest,
) -> Result<serde_json::Value, HttpError> {
    authorize_sidecar_request(app, request)?;
    ensure_ready(app)?;
    let conn = app
        .connection()
        .ok_or_else(|| HttpError::new(503, "Spacetime sidecar is not connected"))?;
    if request.path == "/internal/v1/push-subscription" {
        ensure_json_content_type(request)?;
        let input: PushSubscriptionStatusRequest = decode_json_request(request)?;
        ensure_ticket_scope(app, &input.ticket_id)?;
        if admin_role(&conn, &input.ticket_id, &input.email).is_none() {
            return Err(HttpError::new(403, "forbidden"));
        }
        let subscribed = conn
            .db
            .ticketremote_service_push_subscription()
            .iter()
            .any(|s| {
                s.ticket_id == input.ticket_id
                    && s.email == input.email.trim().to_ascii_lowercase()
                    && s.endpoint == input.endpoint
            });
        return Ok(json!({"subscribed": subscribed}));
    }
    let ticket =
        query_value(&request.query, "ticketId").unwrap_or_else(|| app.cfg.ticket_id.clone());
    ensure_ticket_scope(app, &ticket)?;
    match request.path.as_str() {
        "/internal/v1/monitoring" => {
            let email = query_value(&request.query, "email").unwrap_or_default();
            let role = admin_role(&conn, &ticket, &email)
                .ok_or_else(|| HttpError::new(403, "forbidden"))?;
            let row = conn
                .db
                .ticketremote_service_monitoring_health()
                .iter()
                .find(|r| r.ticket_id == ticket);
            Ok(json!({
                "enabled": row.as_ref().is_some_and(|r| r.enabled),
                "status": row.as_ref().map(|r| if r.status == "unavailable" { "unable_to_verify" } else { r.status.as_str() }).unwrap_or("disabled"),
                "reason": row.as_ref().map(|r| r.reason.as_str()).unwrap_or(""),
                "lastCheckedAtMs": row.as_ref().map(|r| r.last_checked_at_ms).unwrap_or(0),
                "problemStartedAtMs": row.as_ref().map(|r| r.problem_started_at_ms).unwrap_or(0),
                "alertSent": row.as_ref().is_some_and(|r| r.alert_sent),
                "canManageMonitoring": role == "owner",
            }))
        }
        "/internal/v1/push-deliveries" => {
            let deliveries: Vec<_> = conn
                .db
                .ticketremote_service_push_delivery()
                .iter()
                .filter(|r| r.ticket_id == ticket && pending(r))
                .filter_map(|r| delivery_json(&conn, &r))
                .collect();
            Ok(json!({"deliveries": deliveries}))
        }
        "/internal/v1/push-delivery" => {
            let id = query_value(&request.query, "deliveryId").unwrap_or_default();
            let claim = query_value(&request.query, "claimId").unwrap_or_default();
            let clock = Utc::now().timestamp_millis();
            let delivery = conn
                .db
                .ticketremote_service_push_delivery()
                .iter()
                .find(|r| {
                    r.id == id
                        && r.ticket_id == ticket
                        && r.status == "sending"
                        && r.claim_id == claim
                        && !claim.is_empty()
                        && r.claim_until_ms > clock
                })
                .and_then(|r| delivery_json(&conn, &r));
            Ok(json!({"delivery": delivery}))
        }
        _ => Err(HttpError::new(404, "not found")),
    }
}

fn pending(row: &TicketremotePushDelivery) -> bool {
    matches!(row.status.as_str(), "pending" | "sending") && row.attempts < 3
}

fn delivery_json(conn: &DbConnection, row: &TicketremotePushDelivery) -> Option<serde_json::Value> {
    let subscription = conn
        .db
        .ticketremote_service_push_subscription()
        .iter()
        .find(|s| s.id == row.subscription_id)?;
    admin_role(conn, &row.ticket_id, &subscription.email)?;
    let health = conn
        .db
        .ticketremote_service_monitoring_health()
        .iter()
        .find(|h| h.ticket_id == row.ticket_id)?;
    if !health.enabled
        || row.kind == "problem"
            && (health.incident_id != row.incident_id || health.status == "ready")
        || row.kind == "recovery" && health.status != "ready"
    {
        return None;
    }
    Some(json!({
        "id": row.id, "subscriptionId": row.subscription_id, "incidentId": row.incident_id,
        "kind": row.kind, "reason": row.reason, "endpoint": subscription.endpoint,
        "p256dh": subscription.p_256_dh, "auth": subscription.auth, "attempts": row.attempts,
        "nextAttemptAtMs": if row.status == "sending" { row.claim_until_ms } else { row.next_attempt_at_ms },
    }))
}

fn notify(app: &AppState) {
    let mut generation = app
        .monitoring_changed
        .0
        .lock()
        .expect("monitoring lock poisoned");
    *generation = generation.wrapping_add(1);
    app.monitoring_changed.1.notify_all();
}

pub(super) fn events(stream: &mut TcpStream, app: &Arc<AppState>) -> Result<(), String> {
    stream
        .set_write_timeout(Some(Duration::from_secs(5)))
        .map_err(|e| e.to_string())?;
    stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: application/x-ndjson\r\nCache-Control: no-store\r\nConnection: close\r\n\r\n").map_err(|e| e.to_string())?;
    let mut seen = u64::MAX;
    loop {
        ensure_ready(app).map_err(|e| e.message)?;
        let generation = *app
            .monitoring_changed
            .0
            .lock()
            .expect("monitoring lock poisoned");
        if generation != seen {
            stream
                .write_all(b"{\"changed\":true}\n")
                .map_err(|e| e.to_string())?;
            seen = generation;
        } else {
            stream.write_all(b"\n").map_err(|e| e.to_string())?;
        }
        let guard = app
            .monitoring_changed
            .0
            .lock()
            .expect("monitoring lock poisoned");
        let _ = app
            .monitoring_changed
            .1
            .wait_timeout_while(guard, Duration::from_secs(5), |value| *value == seen)
            .expect("monitoring wait poisoned");
    }
}

pub(super) fn callbacks(conn: &DbConnection, app: &Arc<AppState>) {
    // Bounded, best-effort shared logging must never hold up the DB subscription.
    let (sender, receiver) = mpsc::sync_channel::<AppendSafeOperationalLogRequest>(16);
    let log_app = Arc::downgrade(app);
    thread::spawn(move || {
        while let Ok(input) = receiver.recv() {
            let Some(app) = log_app.upgrade() else { break };
            let _ = append_operational_log_input(&app, input);
        }
    });
    let inserted = Arc::clone(app);
    conn.db
        .ticketremote_service_monitoring_health()
        .on_insert(move |_, _| notify(&inserted));
    let updated = Arc::clone(app);
    conn.db
        .ticketremote_service_monitoring_health()
        .on_update(move |_, old, new| {
            notify(&updated);
            if (old.enabled, &old.status, &old.reason) != (new.enabled, &new.status, &new.reason) {
                let _ = sender.try_send(AppendSafeOperationalLogRequest {
                    id: format!(
                        "monitor-{}-{}-{}-{}-{}",
                        new.epoch, new.last_checked_at_ms, new.sequence, new.status, new.reason
                    ),
                    ticket_id: new.ticket_id.clone(),
                    source: "ticket_monitoring".into(),
                    level: "info".into(),
                    event: "ticket_monitoring_changed".into(),
                    correlation_id: new.incident_id.clone(),
                    detail_json:
                        json!({"enabled": new.enabled, "status": new.status, "reason": new.reason})
                            .to_string(),
                    _now_arg: String::new(),
                });
            }
        });
    let inserted = Arc::clone(app);
    conn.db
        .ticketremote_service_push_delivery()
        .on_insert(move |_, _| notify(&inserted));
    let updated = Arc::clone(app);
    conn.db
        .ticketremote_service_push_delivery()
        .on_update(move |_, _, _| notify(&updated));
    let deleted = Arc::clone(app);
    conn.db
        .ticketremote_service_push_delivery()
        .on_delete(move |_, _| notify(&deleted));
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn write_routes_are_private_and_new_subscriptions_are_in_scope() {
        for route in [
            "set-monitoring",
            "set-push-subscription",
            "claim-push-delivery",
            "finish-push-delivery",
        ] {
            assert!(is_write_proxy_route(&format!(
                "/internal/v1/reducers/{route}"
            )));
        }
        let queries = subscription_queries("vivi-default");
        for table in ["monitoring_health", "push_subscription", "push_delivery"] {
            assert!(queries.contains(&format!("SELECT * FROM ticketremote_service_{table}")));
        }
        assert!(!queries
            .iter()
            .any(|q| q == "SELECT * FROM ticketremote_push_subscription"));
    }
}
