// Appended ONLY to a copied module by test-action-statistics.py. Never linked
// into the production module. All identities and phone observations are fake.
use command::ticketremote_command_receipt;

fn fixture_setup(ctx: &ReducerContext, ticket: &str) {
    ticketremote_service_bootstrap(
        ctx,
        ticket.into(),
        "Statistics fixture".into(),
        "fixture@example.test".into(),
        "pixel".into(),
        "http://127.0.0.1:9".into(),
        "fixture".into(),
        "https://example.test".into(),
        "fixture".into(),
    )
    .unwrap();
    let clock = now(ctx);
    upsert_row!(
        ctx,
        ticketremote_phone_control_state,
        TicketremotePhoneControlState {
            id: phone_row_id(ticket, "pixel"),
            ticketId: ticket.into(),
            backendId: "pixel".into(),
            sessionId: "pc-fixture".into(),
            sessionGeneration: 1,
            contextRevision: "pc-fixture:1".into(),
            observationSequence: 1,
            view: "unactivated_detail".into(),
            ready: true,
            busy: false,
            reason: "fixture".into(),
            leftBasisPoints: 100,
            topBasisPoints: 100,
            rightBasisPoints: 5000,
            bottomBasisPoints: 2000,
            observedAt: clock.clone(),
            expiresAt: add_ms(&clock, 3000),
            updatedAt: clock.clone(),
            clockAt: clock,
        }
    );
}

fn fixture_command(
    ctx: &ReducerContext,
    ticket: &str,
    id: &str,
    operation: &str,
    source: &str,
) -> Result<(), String> {
    command::ticketremote_member_command(
        ctx,
        2,
        ticket.into(),
        "pixel".into(),
        id.into(),
        operation.into(),
        "pc-fixture:1".into(),
        now(ctx),
        if operation == "control_code" {
            r#"{"sessionId":"fixture-session","digits":"1234"}"#.into()
        } else {
            serde_json::json!({"source":source,"reason":"user_request"}).to_string()
        },
    )
}

fn fixture_counts(ctx: &ReducerContext, ticket: &str) -> [u32; 4] {
    ctx.db
        .ticketremote_member_daily_actions()
        .ticketDay()
        .filter((ticket,))
        .fold([0; 4], |mut total, row| {
            for (index, counts) in [
                &row.registrationAttempts,
                &row.registrationSuccesses,
                &row.controlCodeAttempts,
                &row.controlCodeSuccesses,
            ]
            .iter()
            .enumerate()
            {
                total[index] += counts.iter().sum::<u32>();
            }
            total
        })
}

fn fixture_finish_registration(ctx: &ReducerContext, ticket: &str, id: &str, status: &str) {
    ticketremote_finalize_ticket_action_v3(
        ctx,
        ticket.into(),
        "pixel".into(),
        ticket_action_v3_command_id(ticket, "pixel", id),
        id.into(),
        "register_current".into(),
        status.into(),
        if status == "succeeded" {
            "activation_proven"
        } else {
            "failed"
        }
        .into(),
        if status == "succeeded" {
            "activated_current"
        } else {
            "unknown"
        }
        .into(),
        "0".into(),
        "0".into(),
        "fixture".into(),
        now(ctx),
        id.into(),
        "pc-fixture:1".into(),
        if status == "succeeded" {
            format!("activation-{id}")
        } else {
            String::new()
        },
        false,
        0,
        0,
        0,
        0,
        now(ctx),
    )
    .unwrap();
}

fn fixture_finish_code(ctx: &ReducerContext, ticket: &str, id: &str, status: &str) {
    ticketremote_update_control_code_request(
        ctx,
        ticket.into(),
        id.into(),
        status.into(),
        "fixture".into(),
        "".into(),
        "1".into(),
        "1".into(),
        "1".into(),
        "1".into(),
        "1".into(),
        "phone_visual_generated_inline".into(),
        now(ctx),
        false,
        now(ctx),
    )
    .unwrap();
}

#[spacetimedb::reducer]
pub fn fixture_statistics_case(ctx: &ReducerContext, case: String) -> Result<(), String> {
    let ticket = format!("stats-{case}");
    fixture_setup(ctx, &ticket);
    match case.as_str() {
        "row-reuse" => {
            for (revision, configured) in [("first", false), ("replacement", true)] {
                let row = upsert_vivi_credential_state(ctx, &ticket, "pixel", configured, revision, &now(ctx));
                let stored = ctx.db.ticketremote_vivi_credential_state().id().find(&row.id).unwrap();
                assert_eq!(row.revision, revision);
                assert_eq!(row.configured, configured);
                assert!(same_fields!(stored, row; id, ticketId, backendId, configured, revision, updatedAt));
            }
            let renamed = ensure_ticket(ctx, &ticket, "Renamed fixture", &now(ctx));
            assert_eq!(renamed.displayName, "Renamed fixture");
            assert_eq!(ctx.db.ticketremote_ticket().id().find(&renamed.id).unwrap().displayName, renamed.displayName);
        }
        "registration" => {
            fixture_command(
                ctx,
                &ticket,
                "register-1",
                "register_current",
                "browser_slider",
            )?;
            fixture_command(
                ctx,
                &ticket,
                "register-1",
                "register_current",
                "browser_slider",
            )?;
            assert_eq!(fixture_counts(ctx, &ticket), [1, 0, 0, 0]);
            fixture_finish_registration(ctx, &ticket, "register-1", "succeeded");
            fixture_finish_registration(ctx, &ticket, "register-1", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [1, 1, 0, 0]);
        }
        "queued" | "queued-rejected" => {
            fixture_command(
                ctx,
                &ticket,
                "queued-1",
                "register_current",
                "browser_slider",
            )?;
            fixture_command(
                ctx,
                &ticket,
                "queued-2",
                "register_current",
                "browser_slider",
            )?;
            fixture_command(
                ctx,
                &ticket,
                "queued-2",
                "register_current",
                "browser_slider",
            )?;
            assert_eq!(fixture_counts(ctx, &ticket), [2, 0, 0, 0]);
            assert_eq!(
                fixture_command(
                    ctx,
                    &ticket,
                    "queued-3",
                    "register_current",
                    "browser_slider"
                ),
                Err("ticket_action_queue_full".into())
            );
            assert_eq!(fixture_counts(ctx, &ticket), [2, 0, 0, 0]);
            if case == "queued" {
                ticketremote_member_set_limit_preference(ctx, ticket.clone(), false)?;
            }
            fixture_finish_registration(ctx, &ticket, "queued-1", "failed");
            let queued = ctx
                .db
                .ticketremote_ticket_action_v3()
                .id()
                .find(ticket_action_v3_row_id(&ticket, "pixel", "queued-2"))
                .unwrap();
            if case == "queued" {
                assert_eq!(
                    queued.status, "pending",
                    "queued result: {} / {}",
                    queued.status, queued.reason
                );
                fixture_finish_registration(ctx, &ticket, "queued-2", "succeeded");
                assert_eq!(fixture_counts(ctx, &ticket), [2, 1, 0, 0]);
            } else {
                assert_eq!(queued.status, "failed");
                assert_eq!(queued.reason, "registration_interval");
                assert_eq!(fixture_counts(ctx, &ticket), [2, 0, 0, 0]);
            }
        }
        "rejected" => {
            let table = ctx.db.ticketremote_phone_control_state();
            let mut row = table.id().find(phone_row_id(&ticket, "pixel")).unwrap();
            row.ready = false;
            table.id().update(row);
            // A stale slider commits a visible rejection while returning Ok.
            fixture_command(
                ctx,
                &ticket,
                "blocked-slider",
                "register_current",
                "browser_slider",
            )?;
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 0, 0]);
            assert!(fixture_command(ctx, &ticket, "blocked-code", "control_code", "").is_err());
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 0, 0]);
        }
        "menu" => {
            fixture_command(
                ctx,
                &ticket,
                "menu-register",
                "register_current",
                "browser_button",
            )?;
            fixture_finish_registration(ctx, &ticket, "menu-register", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 0, 0]);
        }
        "code" => {
            fixture_command(ctx, &ticket, "code-1", "control_code", "")?;
            fixture_command(ctx, &ticket, "code-1", "control_code", "")?;
            fixture_finish_code(ctx, &ticket, "code-1", "succeeded");
            fixture_finish_code(ctx, &ticket, "code-1", "succeeded");
            fixture_finish_code(ctx, &ticket, "code-1", "closed");
            fixture_finish_code(ctx, &ticket, "code-1", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 1, 1]);
            fixture_command(ctx, &ticket, "code-2", "control_code", "")?;
            fixture_finish_code(ctx, &ticket, "code-2", "failed");
            fixture_finish_code(ctx, &ticket, "code-2", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 2, 1]);
        }
        "queued-code" => {
            fixture_command(
                ctx,
                &ticket,
                "blocker",
                "register_current",
                "browser_button",
            )?;
            fixture_command(ctx, &ticket, "queued-code", "control_code", "")?;
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 1, 0]);
            fixture_finish_registration(ctx, &ticket, "blocker", "failed");
            fixture_finish_code(ctx, &ticket, "queued-code", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [0, 0, 1, 1]);
        }
        "original-hour" | "expired" => {
            let command_id = format!("delayed-{case}");
            fixture_command(ctx, &ticket, &command_id, "control_code", "")?;
            let receipts = ctx.db.ticketremote_command_receipt();
            let mut receipt = receipts
                .id()
                .find(ticket_action_v3_row_id(&ticket, "pixel", &command_id))
                .unwrap();
            // Move the synthetic admission to a past Riga day; success still
            // arrives at the real current server time.
            let age_days = if case == "expired" { 31 } else { 1 };
            receipt.createdAt = add_ms(&now(ctx), -age_days * 24 * 60 * 60 * 1000);
            let utc = DateTime::parse_from_rfc3339(&receipt.createdAt)
                .unwrap()
                .with_timezone(&Utc);
            let bucket = member_activity_bucket_with_retention(utc, 30).unwrap();
            receipts.id().update(receipt);
            let table = ctx.db.ticketremote_member_daily_actions();
            let mut row = table.ticketDay().filter((&ticket,)).next().unwrap();
            table.id().delete(&row.id);
            row.id = member_activity_row_id(&ticket, &row.accountScopeId, &bucket.day);
            row.day = bucket.day;
            row.expiresAt = bucket.expires_at;
            row.controlCodeAttempts = vec![0; 24];
            row.controlCodeAttempts[bucket.hour] = 1;
            table.insert(row);
            fixture_finish_code(ctx, &ticket, &command_id, "succeeded");
            assert_eq!(
                fixture_counts(ctx, &ticket),
                [0, 0, 1, if case == "expired" { 0 } else { 1 }]
            );
            if case == "expired" {
                cleanup_expired(ctx, &ticket, &now(ctx), 1000);
                assert_eq!(fixture_counts(ctx, &ticket), [0; 4]);
            } else {
                let row = table.ticketDay().filter((&ticket,)).next().unwrap();
                assert_eq!(row.controlCodeSuccesses[bucket.hour], 1);
            }
        }
        "rollback" => {
            fixture_command(
                ctx,
                &ticket,
                "rollback",
                "register_current",
                "browser_slider",
            )?;
            return Err("fixture_expected_rollback".into());
        }
        "assert-rollback" => {
            assert_eq!(fixture_counts(ctx, "stats-rollback"), [0; 4]);
            assert!(
                ctx.db
                    .ticketremote_command_receipt()
                    .id()
                    .find(ticket_action_v3_row_id(
                        "stats-rollback",
                        "pixel",
                        "rollback"
                    ))
                    .is_none()
            );
        }
        "migration" => {
            let receipt = ctx
                .db
                .ticketremote_command_receipt()
                .id()
                .find(ticket_action_v3_row_id("stats-migration", "pixel", "old"))
                .unwrap();
            assert!(receipt.statisticsKind.is_none());
            assert!(!receipt.statisticsSucceeded);
            let old = ctx
                .db
                .ticketremote_member_daily_activity()
                .ticketDay()
                .filter(("stats-migration",))
                .next()
                .unwrap();
            assert_eq!(old.hourlyTicks.iter().sum::<u32>(), 1);
            action_statistics::record_success(ctx, "stats-migration", "pixel", "old");
            assert_eq!(fixture_counts(ctx, "stats-migration"), [0; 4]);
        }
        _ => return Err("unknown fixture case".into()),
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn fixture_migration_seed(_ctx: &ReducerContext) -> Result<(), String> {
    Err("seed only runs before migration".into())
}
