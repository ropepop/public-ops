//! Foreground viewing samples, idempotent delivery, and Riga calendar buckets.
use super::*;
use std::collections::BTreeMap;

const MAX_ACTIVITY_BATCH_SLOTS: usize = 720;
const ACTIVITY_INGEST_DAYS: u64 = 30;

#[derive(Clone, Debug, Eq, PartialEq)]
pub(super) struct MemberActivityBucket {
    pub(super) day: String,
    pub(super) hour: usize,
    tick_slot: i64,
    day_start_slot: i64,
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

pub(super) fn member_activity_bucket_with_retention(
    utc: DateTime<Utc>,
    retention_days: u64,
) -> Result<MemberActivityBucket, String> {
    let local = utc.with_timezone(&Riga);
    let day = local.date_naive();
    let expiry_day = day
        .checked_add_days(Days::new(retention_days))
        .ok_or_else(|| "activity expiry outside supported range".to_string())?;
    let expires_at = iso(Timestamp::from_micros_since_unix_epoch(
        riga_midnight(expiry_day)?.timestamp_micros(),
    ));
    Ok(MemberActivityBucket {
        day: format!("{:04}-{:02}-{:02}", day.year(), day.month(), day.day()),
        hour: local.hour() as usize,
        tick_slot: utc
            .timestamp_micros()
            .div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS),
        day_start_slot: riga_midnight(day)?
            .timestamp_micros()
            .div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS),
        expires_at,
    })
}

fn riga_midnight(day: chrono::NaiveDate) -> Result<DateTime<Utc>, String> {
    let midnight = day
        .and_hms_opt(0, 0, 0)
        .ok_or_else(|| "activity expiry boundary unavailable".to_string())?;
    let local = match Riga.from_local_datetime(&midnight) {
        LocalResult::Single(value) => value,
        // Europe/Riga has no modern midnight transition, but choosing the
        // earliest occurrence keeps the boundary deterministic if that ever
        // changes in the timezone database.
        LocalResult::Ambiguous(earliest, _) => earliest,
        LocalResult::None => return Err("activity expiry boundary unavailable".into()),
    };
    Ok(local.with_timezone(&Utc))
}

pub(super) fn member_activity_slots(
    timestamp: Timestamp,
    slots: &[i64],
) -> Result<Vec<MemberActivityBucket>, String> {
    if slots.len() > MAX_ACTIVITY_BATCH_SLOTS {
        return Err("activity_slots_too_many".into());
    }
    let current = member_activity_bucket(timestamp)?;
    let oldest_day = DateTime::<Utc>::from_timestamp_micros(timestamp.to_micros_since_unix_epoch())
        .ok_or("activity_slot_invalid")?
        .with_timezone(&Riga)
        .date_naive()
        .checked_sub_days(Days::new(ACTIVITY_INGEST_DAYS - 1))
        .ok_or("activity_slot_invalid")?;
    let oldest_slot = riga_midnight(oldest_day)?
        .timestamp_micros()
        .div_euclid(MEMBER_ACTIVITY_TICK_SLOT_MICROS);
    slots
        .iter()
        .map(|slot| {
            let micros = slot
                .checked_mul(MEMBER_ACTIVITY_TICK_SLOT_MICROS)
                .filter(|_| *slot >= 0)
                .ok_or("activity_slot_invalid")?;
            let utc =
                DateTime::<Utc>::from_timestamp_micros(micros).ok_or("activity_slot_invalid")?;
            if *slot > current.tick_slot {
                return Err("activity_slot_future".into());
            }
            if *slot < oldest_slot {
                return Err("activity_slot_expired".into());
            }
            member_activity_bucket_from_utc(utc)
        })
        .collect()
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
    row: &mut TicketremoteMemberDailyActivity,
    bucket: &MemberActivityBucket,
) -> bool {
    let floor = *row.coverageFloorSlot.get_or_insert_with(|| {
        row.lastTickSlot
            .saturating_add(1)
            .max(bucket.day_start_slot)
    });
    if bucket.tick_slot < floor || bucket.hour >= MEMBER_ACTIVITY_HOURS_PER_DAY {
        return false;
    }
    let offset = (bucket.tick_slot - bucket.day_start_slot) as usize;
    let (byte, mask) = (offset / 8, 1 << (offset % 8));
    let coverage = row.slotCoverage.get_or_insert_with(Vec::new);
    coverage.resize(coverage.len().max(byte + 1), 0);
    if coverage[byte] & mask != 0 {
        return false;
    }
    coverage[byte] |= mask;
    row.hourlyTicks.resize(MEMBER_ACTIVITY_HOURS_PER_DAY, 0);
    row.hourlyTicks.truncate(MEMBER_ACTIVITY_HOURS_PER_DAY);
    row.hourlyTicks[bucket.hour] = row.hourlyTicks[bucket.hour].saturating_add(1);
    let sampled_at = iso(Timestamp::from_micros_since_unix_epoch(
        bucket.tick_slot * MEMBER_ACTIVITY_TICK_SLOT_MICROS,
    ));
    if bucket.tick_slot > row.lastTickSlot {
        row.lastTickAt = sampled_at.clone();
    }
    if row.firstTickAt.is_empty() || sampled_at < row.firstTickAt {
        row.firstTickAt = sampled_at;
    }
    row.lastTickSlot = row.lastTickSlot.max(bucket.tick_slot);
    true
}

pub(super) fn upsert_member_activity_tick(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    observed_at: &str,
    bucket: &MemberActivityBucket,
) {
    upsert_member_activity_slots(
        ctx,
        ticket_id,
        email,
        observed_at,
        std::slice::from_ref(bucket),
    );
}

pub(super) fn upsert_member_activity_slots(
    ctx: &ReducerContext,
    ticket_id: &str,
    email: &str,
    observed_at: &str,
    buckets: &[MemberActivityBucket],
) {
    let ticket_id = clean_ticket_id(ticket_id);
    let account_scope_id = account_scope_id(email);
    let table = ctx.db.ticketremote_member_daily_activity();
    // Each bounded batch rewrites a day at most once, including duplicates and
    // delayed samples delivered in arbitrary order.
    let mut days = BTreeMap::<&str, Vec<&MemberActivityBucket>>::new();
    for bucket in buckets {
        days.entry(&bucket.day).or_default().push(bucket);
    }
    for (day, buckets) in days {
        let id = member_activity_row_id(&ticket_id, &account_scope_id, day);
        let existing = table.id().find(&id);
        let is_new = existing.is_none();
        let mut row = existing.unwrap_or_else(|| TicketremoteMemberDailyActivity {
            id,
            ticketId: ticket_id.clone(),
            accountScopeId: account_scope_id.clone(),
            day: day.into(),
            hourlyTicks: vec![0; MEMBER_ACTIVITY_HOURS_PER_DAY],
            lastTickSlot: i64::MIN,
            firstTickAt: String::new(),
            lastTickAt: String::new(),
            updatedAt: observed_at.into(),
            expiresAt: buckets[0].expires_at.clone(),
            coverageFloorSlot: Some(buckets[0].day_start_slot),
            slotCoverage: None,
        });
        let mut changed = false;
        for bucket in buckets {
            changed |= apply_member_activity_tick(&mut row, bucket);
        }
        if changed {
            row.updatedAt = observed_at.into();
            if is_new {
                table.insert(row);
            } else {
                table.id().update(row);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bucket(
        year: i32,
        month: u32,
        day: u32,
        hour: u32,
        minute: u32,
        second: u32,
    ) -> MemberActivityBucket {
        member_activity_bucket_from_utc(
            Utc.with_ymd_and_hms(year, month, day, hour, minute, second)
                .unwrap(),
        )
        .unwrap()
    }

    fn empty_row(bucket: &MemberActivityBucket) -> TicketremoteMemberDailyActivity {
        TicketremoteMemberDailyActivity {
            id: "fixture".into(),
            ticketId: "fixture".into(),
            accountScopeId: "fixture".into(),
            day: bucket.day.clone(),
            hourlyTicks: vec![0; 24],
            lastTickSlot: i64::MIN,
            firstTickAt: String::new(),
            lastTickAt: String::new(),
            updatedAt: String::new(),
            expiresAt: bucket.expires_at.clone(),
            coverageFloorSlot: Some(bucket.day_start_slot),
            slotCoverage: None,
        }
    }

    #[test]
    fn viewing_samples_deduplicate_out_of_order_and_preserve_legacy_totals() {
        let first = bucket(2026, 9, 11, 10, 0, 0);
        let middle = bucket(2026, 9, 11, 10, 0, 5);
        let last = bucket(2026, 9, 11, 10, 0, 10);
        let mut row = empty_row(&first);
        for sample in [&last, &first, &middle] {
            assert!(apply_member_activity_tick(&mut row, sample));
            assert!(!apply_member_activity_tick(&mut row, sample));
        }
        assert_eq!(row.hourlyTicks.iter().sum::<u32>(), 3);
        assert_eq!(row.lastTickSlot, last.tick_slot);
        assert_eq!(row.firstTickAt, "2026-09-11T10:00:00+00:00");
        assert_eq!(row.lastTickAt, "2026-09-11T10:00:10+00:00");

        row.coverageFloorSlot = None;
        row.slotCoverage = None;
        row.lastTickSlot = first.tick_slot;
        row.hourlyTicks[first.hour] = 91;
        assert!(!apply_member_activity_tick(&mut row, &first));
        assert_eq!(row.coverageFloorSlot, Some(first.tick_slot + 1));
        assert!(apply_member_activity_tick(&mut row, &last));
        assert!(apply_member_activity_tick(&mut row, &middle));
        assert!(!apply_member_activity_tick(&mut row, &last));
        assert_eq!(row.hourlyTicks.iter().sum::<u32>(), 93);
    }

    #[test]
    fn riga_days_and_repeated_dst_hours_have_distinct_slot_coverage() {
        let before = bucket(2026, 9, 11, 20, 59, 59);
        let after = bucket(2026, 9, 11, 21, 0, 0);
        assert_eq!((before.day.as_str(), before.hour), ("2026-09-11", 23));
        assert_eq!((after.day.as_str(), after.hour), ("2026-09-12", 0));
        assert_eq!(after.tick_slot, after.day_start_slot);

        for (start, end, expected_slots) in [
            (
                bucket(2026, 10, 24, 21, 0, 0),
                bucket(2026, 10, 25, 21, 59, 55),
                25 * 720,
            ),
            (
                bucket(2026, 3, 28, 22, 0, 0),
                bucket(2026, 3, 29, 20, 59, 55),
                23 * 720,
            ),
        ] {
            let mut row = empty_row(&start);
            for slot in start.tick_slot..=end.tick_slot {
                let timestamp = Timestamp::from_micros_since_unix_epoch(
                    slot * MEMBER_ACTIVITY_TICK_SLOT_MICROS,
                );
                let sample = member_activity_bucket(timestamp).unwrap();
                assert_eq!(sample.day, start.day);
                assert!(apply_member_activity_tick(&mut row, &sample));
                assert!(!apply_member_activity_tick(&mut row, &sample));
            }
            assert_eq!(row.hourlyTicks.iter().sum::<u32>(), expected_slots);
            assert_eq!(
                row.slotCoverage.as_ref().unwrap().len(),
                expected_slots as usize / 8
            );
            assert_eq!(
                row.hourlyTicks[3],
                if expected_slots == 25 * 720 { 1440 } else { 0 }
            );
        }
    }

    #[test]
    fn batches_reject_invalid_future_expired_or_excessive_slots_before_writing() {
        // March 30 Riga midnight: the preceding 29 dates span the spring jump.
        let now = Timestamp::from_micros_since_unix_epoch(
            Utc.with_ymd_and_hms(2026, 3, 29, 21, 0, 0)
                .unwrap()
                .timestamp_micros(),
        );
        let current = member_activity_bucket(now).unwrap();
        let oldest = bucket(2026, 2, 28, 22, 0, 0).tick_slot;
        assert!(member_activity_slots(now, &[oldest, current.tick_slot, oldest]).is_ok());
        assert!(member_activity_slots(now, &[]).is_ok());
        assert!(member_activity_slots(now, &vec![current.tick_slot; 720]).is_ok());
        for (slots, error) in [
            (vec![current.tick_slot; 721], "activity_slots_too_many"),
            (vec![oldest, -1], "activity_slot_invalid"),
            (vec![i64::MAX], "activity_slot_invalid"),
            (vec![current.tick_slot + 1], "activity_slot_future"),
            (vec![oldest - 1, current.tick_slot], "activity_slot_expired"),
        ] {
            assert_eq!(member_activity_slots(now, &slots).unwrap_err(), error);
        }
    }
}
