//! Server-clock viewing samples and shared Riga calendar buckets.
use super::*;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn viewing_samples_deduplicate_without_backfill_and_follow_riga_days() {
        let mut ticks = vec![0; 24];
        let mut last = i64::MIN;
        assert!(apply_member_activity_tick(&mut ticks, &mut last, 3, 100));
        assert!(!apply_member_activity_tick(&mut ticks, &mut last, 3, 100));
        assert!(!apply_member_activity_tick(&mut ticks, &mut last, 3, 99));
        assert!(!apply_member_activity_tick(&mut ticks, &mut last, 24, 101));
        assert!(apply_member_activity_tick(&mut ticks, &mut last, 4, 112));
        assert_eq!(ticks.iter().sum::<u32>(), 2);
        let before = member_activity_bucket_from_utc(Utc.with_ymd_and_hms(2026, 9, 11, 20, 59, 59).unwrap()).unwrap();
        let after = member_activity_bucket_from_utc(Utc.with_ymd_and_hms(2026, 9, 11, 21, 0, 0).unwrap()).unwrap();
        assert_eq!((before.day.as_str(), before.hour), ("2026-09-11", 23));
        assert_eq!((after.day.as_str(), after.hour), ("2026-09-12", 0));
        // Both occurrences of the repeated autumn hour count distinct server slots.
        let first = member_activity_bucket_from_utc(Utc.with_ymd_and_hms(2026, 10, 25, 0, 30, 0).unwrap()).unwrap();
        let second = member_activity_bucket_from_utc(Utc.with_ymd_and_hms(2026, 10, 25, 1, 30, 0).unwrap()).unwrap();
        assert_eq!((&first.day, first.hour, &first.expires_at), (&second.day, second.hour, &second.expires_at));
        assert_eq!(second.tick_slot - first.tick_slot, 720);
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(super) struct MemberActivityBucket {
    pub(super) day: String,
    pub(super) hour: usize,
    tick_slot: i64,
    pub(super) expires_at: String,
}

pub(super) fn member_activity_bucket(timestamp: Timestamp) -> Result<MemberActivityBucket, String> {
    let micros = timestamp.to_micros_since_unix_epoch();
    let utc = DateTime::<Utc>::from_timestamp_micros(micros)
        .ok_or_else(|| "activity timestamp outside supported range".to_string())?;
    member_activity_bucket_from_utc(utc)
}

fn member_activity_bucket_from_utc(utc: DateTime<Utc>) -> Result<MemberActivityBucket, String> {
    member_activity_bucket_with_retention(utc, MEMBER_ACTIVITY_RETENTION_DAYS)
}

pub(super) fn member_activity_bucket_with_retention(utc: DateTime<Utc>, retention_days: u64) -> Result<MemberActivityBucket, String> {
    let local = utc.with_timezone(&Riga);
    let day = local.date_naive();
    let expiry_day = day
        .checked_add_days(Days::new(retention_days))
        .ok_or_else(|| "activity expiry outside supported range".to_string())?;
    let expiry_midnight = expiry_day
        .and_hms_opt(0, 0, 0)
        .ok_or_else(|| "activity expiry boundary unavailable".to_string())?;
    let expiry_local = match Riga.from_local_datetime(&expiry_midnight) {
        LocalResult::Single(value) => value,
        // Europe/Riga has no modern midnight transition, but choosing the
        // earliest occurrence keeps the boundary deterministic if that ever
        // changes in the timezone database.
        LocalResult::Ambiguous(earliest, _) => earliest,
        LocalResult::None => return Err("activity expiry boundary unavailable".into()),
    };
    let expires_at = iso(Timestamp::from_micros_since_unix_epoch(
        expiry_local.with_timezone(&Utc).timestamp_micros(),
    ));
    Ok(MemberActivityBucket {
        day: format!("{:04}-{:02}-{:02}", day.year(), day.month(), day.day()),
        hour: local.hour() as usize,
        tick_slot: utc
            .timestamp_micros()
            .div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS),
        expires_at,
    })
}

pub(super) fn member_activity_row_id(ticket_id: &str, account_scope_id: &str, day: &str) -> String {
    format!(
        "{}:{}:{}",
        clean_ticket_id(ticket_id),
        account_scope_id.trim(),
        day.trim()
    )
}

fn apply_member_activity_tick(
    hourly_ticks: &mut Vec<u32>,
    last_tick_slot: &mut i64,
    hour: usize,
    tick_slot: i64,
) -> bool {
    if hour >= MEMBER_ACTIVITY_HOURS_PER_DAY || tick_slot <= *last_tick_slot {
        return false;
    }
    hourly_ticks.resize(MEMBER_ACTIVITY_HOURS_PER_DAY, 0);
    hourly_ticks.truncate(MEMBER_ACTIVITY_HOURS_PER_DAY);
    hourly_ticks[hour] = hourly_ticks[hour].saturating_add(1);
    *last_tick_slot = tick_slot;
    true
}

pub(super) fn upsert_member_activity_tick(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    observed_at: &str,
    bucket: &MemberActivityBucket,
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let account_scope_id = account_scope_id(email);
    let id = member_activity_row_id(&ticket_id, &account_scope_id, &bucket.day);
    let table = ctx.db.ticketremote_member_daily_activity();
    if let Some(mut existing) = table.id().find(&id) {
        if !apply_member_activity_tick(
            &mut existing.hourlyTicks,
            &mut existing.lastTickSlot,
            bucket.hour,
            bucket.tick_slot,
        ) {
            return;
        }
        existing.lastTickAt = observed_at.into();
        existing.updatedAt = observed_at.into();
        existing.expiresAt = bucket.expires_at.clone();
        table.id().update(existing);
        return;
    }

    let mut hourly_ticks = vec![0; MEMBER_ACTIVITY_HOURS_PER_DAY];
    let mut last_tick_slot = i64::MIN;
    let accepted = apply_member_activity_tick(
        &mut hourly_ticks,
        &mut last_tick_slot,
        bucket.hour,
        bucket.tick_slot,
    );
    debug_assert!(accepted);
    table.insert(TicketremoteMemberDailyActivity {
        id,
        ticketId: ticket_id,
        accountScopeId: account_scope_id,
        day: bucket.day.clone(),
        hourlyTicks: hourly_ticks,
        lastTickSlot: last_tick_slot,
        firstTickAt: observed_at.into(),
        lastTickAt: observed_at.into(),
        updatedAt: observed_at.into(),
        expiresAt: bucket.expires_at.clone(),
    });
}
