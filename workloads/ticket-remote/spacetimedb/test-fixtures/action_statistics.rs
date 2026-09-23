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
    let action = ctx.db.ticketremote_ticket_action_v3().id().find(ticket_action_v3_row_id(ticket, "pixel", id)).unwrap();
    let revision = if action.target == "register_current" { "pc-fixture:1" } else { id };
    ticketremote_finalize_ticket_action_v3(
        ctx,
        ticket.into(),
        "pixel".into(),
        ticket_action_v3_command_id(ticket, "pixel", id),
        id.into(),
        action.target,
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
        revision.into(),
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
        "queued" | "queued-rejected" | "queued-menu" | "queued-latest" => {
            let operation = if case == "queued-latest" { "open_latest_and_register" } else { "register_current" };
            let source = if case == "queued" || case == "queued-rejected" { "browser_slider" } else { "browser_button" };
            fixture_command(
                ctx,
                &ticket,
                "queued-1",
                operation,
                source,
            )?;
            fixture_command(
                ctx,
                &ticket,
                "queued-2",
                operation,
                source,
            )?;
            fixture_command(
                ctx,
                &ticket,
                "queued-2",
                operation,
                source,
            )?;
            assert_eq!(fixture_counts(ctx, &ticket), [2, 0, 0, 0]);
            assert_eq!(
                fixture_command(
                    ctx,
                    &ticket,
                    "queued-3",
                    operation,
                    source
                ),
                Err("ticket_action_queue_full".into())
            );
            assert_eq!(fixture_counts(ctx, &ticket), [2, 0, 0, 0]);
            if case != "queued-rejected" {
                ticketremote_member_set_limit_preference(ctx, ticket.clone(), false)?;
            }
            fixture_finish_registration(ctx, &ticket, "queued-1", "failed");
            let queued = ctx
                .db
                .ticketremote_ticket_action_v3()
                .id()
                .find(ticket_action_v3_row_id(&ticket, "pixel", "queued-2"))
                .unwrap();
            if case != "queued-rejected" {
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
        "menu" | "menu-latest" => {
            let operation = if case == "menu" { "register_current" } else { "open_latest_and_register" };
            fixture_command(
                ctx,
                &ticket,
                "menu-register",
                operation,
                "browser_button",
            )?;
            fixture_command(ctx, &ticket, "menu-register", operation, "browser_button")?;
            assert_eq!(fixture_counts(ctx, &ticket), [1, 0, 0, 0]);
            fixture_finish_registration(ctx, &ticket, "menu-register", "succeeded");
            fixture_finish_registration(ctx, &ticket, "menu-register", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [1, 1, 0, 0]);
            fixture_command(ctx, &ticket, "menu-rate-limited", operation, "browser_button")?;
            assert_eq!(fixture_counts(ctx, &ticket), [1, 1, 0, 0]);
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
            assert_eq!(fixture_counts(ctx, &ticket), [1, 0, 1, 0]);
            fixture_finish_registration(ctx, &ticket, "blocker", "failed");
            fixture_finish_code(ctx, &ticket, "queued-code", "succeeded");
            assert_eq!(fixture_counts(ctx, &ticket), [1, 0, 1, 1]);
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
            assert!(old.coverageFloorSlot.is_none());
            assert!(old.slotCoverage.is_none());
            ticketremote_record_member_activity_slots(ctx, "stats-migration".into(), "fixture@example.test".into(), vec![old.lastTickSlot, old.lastTickSlot + 1, old.lastTickSlot + 1])?;
            let migrated = ctx.db.ticketremote_member_daily_activity().id().find(&old.id).unwrap();
            assert_eq!(migrated.hourlyTicks.iter().sum::<u32>(), 2);
            assert_eq!(migrated.coverageFloorSlot, Some(old.lastTickSlot + 1));
            assert!(migrated.slotCoverage.is_some());
            action_statistics::record_success(ctx, "stats-migration", "pixel", "old");
            assert_eq!(fixture_counts(ctx, "stats-migration"), [0; 4]);
        }
        "activity" => {
            let slot = ctx.timestamp.to_micros_since_unix_epoch().div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS);
            let email = "fixture@example.test".to_string();
            ticketremote_record_member_activity_slots(ctx, ticket.clone(), email.clone(), vec![])?;
            assert!(ctx.db.ticketremote_member_daily_activity().ticketDay().filter((&ticket,)).next().is_none());
            // These calls model overlapping tabs, lost acknowledgements, and
            // offline samples arriving after a newer sample was committed.
            ticketremote_record_member_activity_slots(ctx, ticket.clone(), email.clone(), vec![slot - 1, slot - 1])?;
            ticketremote_record_member_activity_slots(ctx, ticket.clone(), email.clone(), vec![slot - 3, slot - 2, slot - 1])?;
            ticketremote_member_record_activity_tick(ctx, ticket.clone())?;
            ticketremote_record_member_activity_slots(ctx, ticket.clone(), email.clone(), vec![slot])?;
            let counts = || ctx.db.ticketremote_member_daily_activity().ticketDay().filter((&ticket,)).map(|row| row.hourlyTicks.iter().sum::<u32>()).sum::<u32>();
            assert_eq!(counts(), 4);
            for (slots, expected) in [
                (vec![slot - 4, slot + 1], "activity_slot_future"),
                (vec![slot - 4, -1], "activity_slot_invalid"),
                (vec![slot - 30 * 24 * 720], "activity_slot_expired"),
                (vec![slot; 721], "activity_slots_too_many"),
            ] {
                assert_eq!(ticketremote_record_member_activity_slots(ctx, ticket.clone(), email.clone(), slots).unwrap_err(), expected);
                assert_eq!(counts(), 4);
            }
            assert_eq!(ticketremote_record_member_activity_slots(ctx, ticket.clone(), "removed@example.test".into(), vec![slot]).unwrap_err(), "member_not_active");
            assert_eq!(ticketremote_record_member_activity_slots(ctx, ticket.clone(), "removed@example.test".into(), vec![]).unwrap_err(), "member_not_active");
            let midnight_ticket = format!("{ticket}-midnight");
            fixture_setup(ctx, &midnight_ticket);
            let utc = DateTime::<Utc>::from_timestamp_micros(ctx.timestamp.to_micros_since_unix_epoch()).unwrap();
            let midnight = Riga.from_local_datetime(&utc.with_timezone(&Riga).date_naive().and_hms_opt(0, 0, 0).unwrap()).earliest().unwrap().timestamp_micros().div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS);
            for _ in 0..2 {
                ticketremote_record_member_activity_slots(ctx, midnight_ticket.clone(), email.clone(), vec![midnight, midnight - 1])?;
            }
            let days: Vec<_> = ctx.db.ticketremote_member_daily_activity().ticketDay().filter((&midnight_ticket,)).collect();
            assert_eq!(days.len(), 2);
            assert!(days.iter().all(|day| day.hourlyTicks.iter().sum::<u32>() == 1));
        }
        _ => return Err("unknown fixture case".into()),
    }
    Ok(())
}

#[spacetimedb::reducer]
pub fn fixture_migration_seed(_ctx: &ReducerContext) -> Result<(), String> {
    Err("seed only runs before migration".into())
}
