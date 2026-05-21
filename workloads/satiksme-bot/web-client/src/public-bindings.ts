import {
  DbConnectionBuilder as __DbConnectionBuilder,
  DbConnectionImpl as __DbConnectionImpl,
  SubscriptionBuilderImpl as __SubscriptionBuilderImpl,
  makeQueryBuilder as __makeQueryBuilder,
  procedures as __procedures,
  reducers as __reducers,
  schema as __schema,
  t as __t,
  table as __table,
  type DbConnectionConfig as __DbConnectionConfig,
  type ErrorContextInterface as __ErrorContextInterface,
  type EventContextInterface as __EventContextInterface,
  type QueryBuilder as __QueryBuilder,
  type RemoteModule as __RemoteModule,
  type SubscriptionEventContextInterface as __SubscriptionEventContextInterface,
  type SubscriptionHandleImpl as __SubscriptionHandleImpl,
} from "spacetimedb";

import PublicAreaReportRow from "./generated/satiksmebot_public_area_report_table";
import PublicIncidentCommentRow from "./generated/satiksmebot_public_incident_comment_table";
import PublicIncidentEventRow from "./generated/satiksmebot_public_incident_event_table";
import PublicLiveSnapshotStateRow from "./generated/satiksmebot_public_live_snapshot_state_table";
import PublicStopSightingRow from "./generated/satiksmebot_public_stop_sighting_table";
import PublicVehicleSightingRow from "./generated/satiksmebot_public_vehicle_sighting_table";

const PublicVehicleContextDoc = __t.object("SatiksmeVehicleContextDoc", {
  stopId: __t.string(),
  stopName: __t.string(),
  mode: __t.string(),
  routeLabel: __t.string(),
  direction: __t.string(),
  destination: __t.string(),
  departureSeconds: __t.u32(),
});

const PublicAreaContextDoc = __t.object("SatiksmeAreaContextDoc", {
  latitude: __t.f64(),
  longitude: __t.f64(),
  radiusMeters: __t.u32(),
  description: __t.string(),
});

const PublicIncidentRow = __t.row({
  id: __t.string().primaryKey(),
  scope: __t.string(),
  subjectId: __t.string(),
  subjectName: __t.string(),
  stopId: __t.string(),
  lastReportName: __t.string(),
  lastReportAt: __t.string(),
  lastReporter: __t.string(),
  commentCount: __t.u32(),
  ongoingVotes: __t.u32(),
  clearedVotes: __t.u32(),
  active: __t.bool(),
  resolved: __t.bool(),
  get vehicle() {
    return __t.option(PublicVehicleContextDoc);
  },
  get area() {
    return __t.option(PublicAreaContextDoc);
  },
});

const tablesSchema = __schema({
  satiksmebot_public_area_report: __table({
    name: "satiksmebot_public_area_report",
    indexes: [
      { accessor: "createdAt", name: "satiksmebot_public_area_report_created_at_idx_btree", algorithm: "btree", columns: ["createdAt"] },
      { accessor: "id", name: "satiksmebot_public_area_report_id_idx_btree", algorithm: "btree", columns: ["id"] },
      { accessor: "incidentId", name: "satiksmebot_public_area_report_incident_id_idx_btree", algorithm: "btree", columns: ["incidentId"] },
    ],
    constraints: [
      { name: "satiksmebot_public_area_report_id_key", constraint: "unique", columns: ["id"] },
    ],
  }, PublicAreaReportRow),
  satiksmebot_public_incident: __table({
    name: "satiksmebot_public_incident",
    indexes: [
      { accessor: "id", name: "satiksmebot_public_incident_id_idx_btree", algorithm: "btree", columns: ["id"] },
      { accessor: "lastReportAt", name: "satiksmebot_public_incident_last_report_at_idx_btree", algorithm: "btree", columns: ["lastReportAt"] },
      { accessor: "scope", name: "satiksmebot_public_incident_scope_idx_btree", algorithm: "btree", columns: ["scope"] },
      { accessor: "stopId", name: "satiksmebot_public_incident_stop_id_idx_btree", algorithm: "btree", columns: ["stopId"] },
      { accessor: "subjectId", name: "satiksmebot_public_incident_subject_id_idx_btree", algorithm: "btree", columns: ["subjectId"] },
    ],
    constraints: [
      { name: "satiksmebot_public_incident_id_key", constraint: "unique", columns: ["id"] },
    ],
  }, PublicIncidentRow),
  satiksmebot_public_incident_comment: __table({
    name: "satiksmebot_public_incident_comment",
    indexes: [
      { accessor: "createdAt", name: "satiksmebot_public_incident_comment_created_at_idx_btree", algorithm: "btree", columns: ["createdAt"] },
      { accessor: "id", name: "satiksmebot_public_incident_comment_id_idx_btree", algorithm: "btree", columns: ["id"] },
      { accessor: "incidentId", name: "satiksmebot_public_incident_comment_incident_id_idx_btree", algorithm: "btree", columns: ["incidentId"] },
    ],
    constraints: [
      { name: "satiksmebot_public_incident_comment_id_key", constraint: "unique", columns: ["id"] },
    ],
  }, PublicIncidentCommentRow),
  satiksmebot_public_incident_event: __table({
    name: "satiksmebot_public_incident_event",
    indexes: [
      { accessor: "createdAt", name: "satiksmebot_public_incident_event_created_at_idx_btree", algorithm: "btree", columns: ["createdAt"] },
      { accessor: "id", name: "satiksmebot_public_incident_event_id_idx_btree", algorithm: "btree", columns: ["id"] },
      { accessor: "incidentId", name: "satiksmebot_public_incident_event_incident_id_idx_btree", algorithm: "btree", columns: ["incidentId"] },
    ],
    constraints: [
      { name: "satiksmebot_public_incident_event_id_key", constraint: "unique", columns: ["id"] },
    ],
  }, PublicIncidentEventRow),
  satiksmebot_public_live_snapshot_state: __table({
    name: "satiksmebot_public_live_snapshot_state",
    indexes: [
      { accessor: "feed", name: "satiksmebot_public_live_snapshot_state_feed_idx_btree", algorithm: "btree", columns: ["feed"] },
      { accessor: "updatedAt", name: "satiksmebot_public_live_snapshot_state_updated_at_idx_btree", algorithm: "btree", columns: ["updatedAt"] },
    ],
    constraints: [
      { name: "satiksmebot_public_live_snapshot_state_feed_key", constraint: "unique", columns: ["feed"] },
    ],
  }, PublicLiveSnapshotStateRow),
  satiksmebot_public_stop_sighting: __table({
    name: "satiksmebot_public_stop_sighting",
    indexes: [
      { accessor: "createdAt", name: "satiksmebot_public_stop_sighting_created_at_idx_btree", algorithm: "btree", columns: ["createdAt"] },
      { accessor: "id", name: "satiksmebot_public_stop_sighting_id_idx_btree", algorithm: "btree", columns: ["id"] },
      { accessor: "incidentId", name: "satiksmebot_public_stop_sighting_incident_id_idx_btree", algorithm: "btree", columns: ["incidentId"] },
      { accessor: "stopId", name: "satiksmebot_public_stop_sighting_stop_id_idx_btree", algorithm: "btree", columns: ["stopId"] },
    ],
    constraints: [
      { name: "satiksmebot_public_stop_sighting_id_key", constraint: "unique", columns: ["id"] },
    ],
  }, PublicStopSightingRow),
  satiksmebot_public_vehicle_sighting: __table({
    name: "satiksmebot_public_vehicle_sighting",
    indexes: [
      { accessor: "createdAt", name: "satiksmebot_public_vehicle_sighting_created_at_idx_btree", algorithm: "btree", columns: ["createdAt"] },
      { accessor: "id", name: "satiksmebot_public_vehicle_sighting_id_idx_btree", algorithm: "btree", columns: ["id"] },
      { accessor: "incidentId", name: "satiksmebot_public_vehicle_sighting_incident_id_idx_btree", algorithm: "btree", columns: ["incidentId"] },
      { accessor: "mode", name: "satiksmebot_public_vehicle_sighting_mode_idx_btree", algorithm: "btree", columns: ["mode"] },
      { accessor: "routeLabel", name: "satiksmebot_public_vehicle_sighting_route_label_idx_btree", algorithm: "btree", columns: ["routeLabel"] },
      { accessor: "stopId", name: "satiksmebot_public_vehicle_sighting_stop_id_idx_btree", algorithm: "btree", columns: ["stopId"] },
    ],
    constraints: [
      { name: "satiksmebot_public_vehicle_sighting_id_key", constraint: "unique", columns: ["id"] },
    ],
  }, PublicVehicleSightingRow),
});

const reducersSchema = __reducers();
const proceduresSchema = __procedures();

const REMOTE_MODULE = {
  versionInfo: {
    cliVersion: "2.1.0" as const,
  },
  tables: tablesSchema.schemaType.tables,
  reducers: reducersSchema.reducersType.reducers,
  ...proceduresSchema,
} satisfies __RemoteModule<
  typeof tablesSchema.schemaType,
  typeof reducersSchema.reducersType,
  typeof proceduresSchema
>;

export const tables: __QueryBuilder<typeof tablesSchema.schemaType> = __makeQueryBuilder(tablesSchema.schemaType);

export type EventContext = __EventContextInterface<typeof REMOTE_MODULE>;
export type SubscriptionEventContext = __SubscriptionEventContextInterface<typeof REMOTE_MODULE>;
export type ErrorContext = __ErrorContextInterface<typeof REMOTE_MODULE>;
export type SubscriptionHandle = __SubscriptionHandleImpl<typeof REMOTE_MODULE>;

export class SubscriptionBuilder extends __SubscriptionBuilderImpl<typeof REMOTE_MODULE> {}

export class DbConnectionBuilder extends __DbConnectionBuilder<DbConnection> {}

export class DbConnection extends __DbConnectionImpl<typeof REMOTE_MODULE> {
  static builder = (): DbConnectionBuilder => {
    return new DbConnectionBuilder(REMOTE_MODULE, (config: __DbConnectionConfig<typeof REMOTE_MODULE>) => new DbConnection(config));
  };

  override subscriptionBuilder = (): SubscriptionBuilder => {
    return new SubscriptionBuilder(this);
  };
}
