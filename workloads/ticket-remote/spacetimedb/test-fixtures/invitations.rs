// Only appended to a disposable module copy by tests/test_invitations.py.
use invitations::*;
#[spacetimedb::table(accessor = fixture_auth)]
pub struct FixtureAuth {
    #[primary_key] identity: Identity,
    payload: String,
}
#[spacetimedb::reducer]
pub fn fixture_bind(ctx: &ReducerContext, kind: String, actor: String, session: String) -> Result<(), String> {
    let payload = match kind.as_str() {
        "service" => serde_json::json!({"iss":SERVICE_OIDC_ISSUER,"aud":SERVICE_OIDC_AUDIENCE,
            "sub":SERVICE_OIDC_SUBJECT,"roles":[SERVICE_ROLE]}),
        "guest" => serde_json::json!({"iss":SERVICE_OIDC_ISSUER,"aud":SERVICE_OIDC_AUDIENCE,
            "sub":format!("guest:{actor}:{session}"),"roles":["ticketremote_guest_proxy"],"invitation_id":actor,"trial_session_id":session}),
        "member" => serde_json::json!({"iss":SERVICE_OIDC_ISSUER,"aud":SERVICE_OIDC_AUDIENCE,
            "sub":format!("member:{actor}"),"roles":[MEMBER_PROXY_ROLE],"email":actor,"email_verified":true}),
        _ => serde_json::json!({}),
    };
    let row = FixtureAuth { identity: ctx.sender(), payload: payload.to_string() };
    if ctx.db.fixture_auth().identity().find(ctx.sender()).is_some() { ctx.db.fixture_auth().identity().update(row); }
    else { ctx.db.fixture_auth().insert(row); }
    if kind == "guest" { invitations::connect_guest(ctx)?; }
    if kind == "member" { upsert_member_identity(ctx, DEFAULT_TICKET_ID, &actor, &now(ctx)); }
    Ok(())
}
#[spacetimedb::reducer]
pub fn fixture_age_invitation(ctx: &ReducerContext, invitation: String) {
    let mut row = ctx.db.ticketremote_invitation().id().find(invitation).unwrap();
    row.expiresAt = add_ms(&now(ctx), -1);
    ctx.db.ticketremote_invitation().id().update(row);
}
#[spacetimedb::reducer]
pub fn fixture_reserve_action(ctx: &ReducerContext, command: String, operation: String, rollback: bool) -> Result<(), String> {
    let actor = invitations::viewer_actor(ctx, DEFAULT_TICKET_ID)?;
    invitations::reserve_action(ctx, DEFAULT_TICKET_ID, "pixel", &command, &actor, &operation)?;
    if rollback { return Err("fixture_rollback".into()); }
    Ok(())
}
#[spacetimedb::reducer]
pub fn fixture_settle_action(ctx: &ReducerContext, command: String, succeeded: bool, provenFailure: bool) -> Result<(), String> {
    require_service(ctx)?;
    invitations::settle_action(ctx, DEFAULT_TICKET_ID, &command, succeeded, provenFailure);
    Ok(())
}
#[spacetimedb::reducer]
pub fn fixture_deactivate(ctx: &ReducerContext, email: String) -> Result<(), String> {
    require_service(ctx)?;
    deactivate_member_row(ctx, DEFAULT_TICKET_ID, &email, &now(ctx));
    Ok(())
}
#[spacetimedb::reducer]
pub fn fixture_manual_member(ctx: &ReducerContext, email: String) -> Result<(), String> {
    require_service(ctx)?;
    authorize_and_upsert_member(ctx, DEFAULT_TICKET_ID, "owner@example.test", &email, "member", &now(ctx))
}
#[spacetimedb::reducer]
pub fn fixture_command(ctx: &ReducerContext, id: String, operation: String) -> Result<(), String> {
    command::ticketremote_member_command(ctx, 2, DEFAULT_TICKET_ID.into(), "pixel".into(), id,
        operation, "pc-fixture:1".into(), now(ctx), r#"{"source":"browser_button","reason":"user_request"}"#.into())
}
#[spacetimedb::reducer]
pub fn fixture_no_guest_members(ctx: &ReducerContext) {
    assert!(!ctx.db.ticketremote_ticket_member().iter().any(|row| row.email.starts_with("guest:")));
}
