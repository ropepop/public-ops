//! Presence stays available to its internal owners, never to ordinary viewers.
use super::*;

fn privileged_ticket(ctx: &ViewContext) -> Option<String> {
    if let Some(ticket) = service_ticket_id_for_viewer(ctx) { return Some(ticket); }
    let binding = member_view_binding(ctx, false)?;
    ctx.db.ticketremote_ticket_member().id().find(member_id(&binding.ticketId, &binding.email))
        .filter(|row| matches!(row.role.as_str(), "owner" | "admin"))
        .map(|_| binding.ticketId)
}

#[spacetimedb::view(accessor = ticketremote_privileged_viewers, public, primary_key = id)]
pub fn viewers(ctx: &ViewContext) -> Vec<TicketremoteStreamViewerFocus> {
    let Some(ticket) = privileged_ticket(ctx) else { return vec![]; };
    ctx.db.ticketremote_stream_viewer_focus().ticketId().filter(&ticket).collect()
}

#[spacetimedb::view(accessor = ticketremote_service_stream_desired_state, public, primary_key = id)]
pub fn desired(ctx: &ViewContext) -> Vec<TicketremoteStreamDesiredState> {
    let Some(ticket) = privileged_ticket(ctx) else { return vec![]; };
    ctx.db.ticketremote_stream_desired_state().ticketBackend().filter((&ticket,)).collect()
}

#[spacetimedb::view(accessor = ticketremote_service_phone_current_report, public, primary_key = id)]
pub fn phone(ctx: &ViewContext) -> Vec<TicketremotePhoneCurrentReport> {
    let Some(ticket) = privileged_ticket(ctx) else { return vec![]; };
    ctx.db.ticketremote_phone_current_report().id().find(phone_row_id(&ticket, "pixel")).into_iter().collect()
}

#[spacetimedb::view(accessor = ticketremote_privileged_relay_report, public, primary_key = id)]
pub fn relay(ctx: &ViewContext) -> Vec<TicketremoteRelayCurrentReport> {
    let Some(ticket) = privileged_ticket(ctx) else { return vec![]; };
    ctx.db.ticketremote_relay_current_report().id().find(phone_row_id(&ticket, "pixel")).into_iter().collect()
}

#[derive(Clone, SpacetimeType)]
pub struct TicketremoteMemberStreamState {
    pub id: String,
    pub coldRestartId: Option<String>,
    pub coldRestartPhase: Option<String>,
    pub coldRestartStartedAt: Option<String>,
    pub coldRestartError: Option<String>,
}

#[spacetimedb::view(accessor = ticketremote_member_stream_state, public, primary_key = id)]
pub fn member_stream(ctx: &ViewContext) -> Vec<TicketremoteMemberStreamState> {
    let Some(binding) = member_view_binding(ctx, false) else { return vec![]; };
    ctx.db.ticketremote_stream_desired_state().ticketBackend().filter((&binding.ticketId,))
        .map(|row| TicketremoteMemberStreamState { id: row.id, coldRestartId: row.coldRestartId,
            coldRestartPhase: row.coldRestartPhase, coldRestartStartedAt: row.coldRestartStartedAt,
            coldRestartError: row.coldRestartError }).collect()
}
