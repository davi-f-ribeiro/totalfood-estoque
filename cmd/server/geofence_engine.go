package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// All distances use geographic WGS84 coordinates; no external map service is needed.
func geofenceDistanceM(lat1, lng1, lat2, lng2 float64) float64 {
	const earth = 6371008.8
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Pow(math.Sin(dLng/2), 2)
	return earth * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(math.Max(0, 1-a)))
}

// A geofence transition requires an observed inside origin and two distinct
// valid fixes across the boundary. On entry, two inside fixes are required.
// eventAt is the first candidate fix, not the time the worker executed.
type geofenceState struct {
	InsideSeen  bool
	Count       int
	CandidateAt time.Time
}

func (s geofenceState) advance(inside, exiting bool, at time.Time) (geofenceState, bool, time.Time) {
	if exiting {
		if inside {
			s.InsideSeen = true
			s.Count = 0
			s.CandidateAt = time.Time{}
			return s, false, time.Time{}
		}
		if !s.InsideSeen {
			return s, false, time.Time{}
		}
	} else if !inside {
		s.Count = 0
		s.CandidateAt = time.Time{}
		return s, false, time.Time{}
	}
	if s.Count > 0 && at.Sub(s.CandidateAt) > 5*time.Minute {
		s.Count = 0 // separated fixes do not confirm a crossing
	}
	if s.Count == 0 {
		s.CandidateAt = at
	}
	s.Count++
	return s, s.Count >= 2, s.CandidateAt
}

// Only timestamps with an explicit timezone are valid without additional
// provider configuration. A timestamp without timezone requires the configured
// device timezone; receiving time is never substituted for GPS time.
func parseRastroGPSTime(raw, zone string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05 -0700"} {
		if t, e := time.Parse(layout, raw); e == nil {
			return t.UTC(), true
		}
	}
	if zone == "" {
		return time.Time{}, false
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "02/01/2006 15:04:05", "2006-01-02 15:04"} {
		if t, e := time.ParseInLocation(layout, raw, loc); e == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

type geofenceTarget struct {
	RouteID, VehicleID, StopID int64
	Lat, Lng                   float64
	Radius                     float64
	ReleasedAt                 time.Time
}
type geofenceFix struct {
	ID       int64
	Lat, Lng float64
	At       time.Time
}

// Process route and stop geofences independently of manual delivery status.
// All state and event writes are in a transaction protected by route row lock.
func (a *app) detectGeofence(ctx context.Context) error {
	rows, err := a.db.QueryContext(ctx, `
SELECT r.id,r.vehicle_id,COALESCE(r.released_at,r.started_at),0::bigint,p.latitude::float8,p.longitude::float8,p.geofence_radius_m::float8
FROM routes r
JOIN LATERAL (
  SELECT MIN(o.origin_warehouse_id) AS warehouse_id,COUNT(DISTINCT o.origin_warehouse_id) AS n
  FROM route_stops s JOIN delivery_orders o ON o.id=s.order_id
  WHERE s.route_id=r.id
) orig ON orig.n=1
JOIN LATERAL (
  SELECT latitude,longitude,geofence_radius_m
  FROM places WHERE warehouse_id=orig.warehouse_id AND kind='deposito' AND active AND latitude IS NOT NULL
  ORDER BY id LIMIT 1
) p ON TRUE
WHERE r.status IN ('liberado','em_andamento') AND COALESCE(r.released_at,r.started_at) IS NOT NULL
  AND r.actual_departure_at IS NULL AND r.day BETWEEN (now() AT TIME ZONE 'America/Fortaleza')::date-1 AND (now() AT TIME ZONE 'America/Fortaleza')::date+1
UNION ALL
SELECT r.id,r.vehicle_id,COALESCE(r.released_at,r.started_at),o.id,p.latitude::float8,p.longitude::float8,p.geofence_radius_m::float8
FROM routes r JOIN route_stops s ON s.route_id=r.id
JOIN delivery_orders o ON o.id=s.order_id JOIN places p ON p.id=o.place_id
WHERE r.status IN ('liberado','em_andamento') AND COALESCE(r.released_at,r.started_at) IS NOT NULL
  AND p.latitude IS NOT NULL AND r.day BETWEEN (now() AT TIME ZONE 'America/Fortaleza')::date-1 AND (now() AT TIME ZONE 'America/Fortaleza')::date+1
  AND NOT EXISTS (SELECT 1 FROM geofence_events e WHERE e.route_id=r.id AND e.stop_order_id=o.id AND e.kind='chegada_cliente')
ORDER BY 1,4`)
	if err != nil {
		return err
	}
	targets := []geofenceTarget{}
	for rows.Next() {
		var t geofenceTarget
		if e := rows.Scan(&t.RouteID, &t.VehicleID, &t.ReleasedAt, &t.StopID, &t.Lat, &t.Lng, &t.Radius); e != nil {
			rows.Close()
			return e
		}
		targets = append(targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, t := range targets {
		if err := a.processGeofenceTarget(ctx, t); err != nil {
			return fmt.Errorf("route %d stop %d: %w", t.RouteID, t.StopID, err)
		}
	}
	return nil
}
func (a *app) processGeofenceTarget(ctx context.Context, t geofenceTarget) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var routeStatus string
	var routeVehicle int64
	var release time.Time
	var departed sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT status,vehicle_id,COALESCE(released_at,started_at),actual_departure_at FROM routes WHERE id=$1 FOR UPDATE`, t.RouteID).Scan(&routeStatus, &routeVehicle, &release, &departed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if routeVehicle != t.VehicleID || (routeStatus != "liberado" && routeStatus != "em_andamento") || (t.StopID == 0 && departed.Valid) {
		return nil
	}
	if t.StopID > 0 {
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM geofence_events WHERE route_id=$1 AND stop_order_id=$2 AND kind='chegada_cliente')`, t.RouteID, t.StopID).Scan(&exists)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	var cursor sql.NullInt64
	var lastGPSTime sql.NullTime
	var insideSeen bool
	var count int
	var candidate sql.NullTime
	var firstPositionID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT last_position_id,last_gps_at,inside_seen,confirmation_count,candidate_at,candidate_position_id FROM geofence_progress WHERE route_id=$1 AND stop_order_id=$2 FOR UPDATE`, t.RouteID, t.StopID).Scan(&cursor, &lastGPSTime, &insideSeen, &count, &candidate, &firstPositionID)
	if errors.Is(err, sql.ErrNoRows) {
		// An origin observed shortly before release anchors the exit check.
		if t.StopID == 0 {
			var lat, lng float64
			e := tx.QueryRowContext(ctx, `SELECT latitude::float8,longitude::float8 FROM geofence_positions
WHERE vehicle_id=$1 AND gps_at BETWEEN $2::timestamptz-interval '5 minutes' AND $2 ORDER BY gps_at DESC,id DESC LIMIT 1`, t.VehicleID, release).Scan(&lat, &lng)
			if e == nil && geofenceDistanceM(lat, lng, t.Lat, t.Lng) <= t.Radius {
				insideSeen = true
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
	} else if err != nil {
		return err
	}
	st := geofenceState{InsideSeen: insideSeen, Count: count}
	if candidate.Valid {
		st.CandidateAt = candidate.Time
	}
	fixes, err := tx.QueryContext(ctx, `SELECT id,latitude::float8,longitude::float8,gps_at
FROM geofence_positions WHERE vehicle_id=$1 AND id>$2 AND gps_at>=$3
ORDER BY id LIMIT 300`, t.VehicleID, nullableCursor(cursor), release)
	if err != nil {
		return err
	}
	list := []geofenceFix{}
	for fixes.Next() {
		var p geofenceFix
		if err := fixes.Scan(&p.ID, &p.Lat, &p.Lng, &p.At); err != nil {
			fixes.Close()
			return err
		}
		list = append(list, p)
	}
	err = fixes.Err()
	fixes.Close()
	if err != nil {
		return err
	}
	for _, p := range list {
		cursor = sql.NullInt64{Int64: p.ID, Valid: true}
		// Reject future/device-clock skew and stale fixes at collection time.
		if p.At.After(time.Now().Add(2*time.Minute)) || time.Since(p.At) > 5*time.Minute {
			continue
		}
		// Different rows with the same device timestamp cannot confirm a crossing.
		// Reject out-of-order data as well; this cursor belongs to this route/stop.
		if lastGPSTime.Valid && !p.At.After(lastGPSTime.Time) {
			continue
		}
		lastGPSTime = sql.NullTime{Time: p.At, Valid: true}
		distance := geofenceDistanceM(p.Lat, p.Lng, t.Lat, t.Lng)
		// Margin mitigates boundary oscillation from GPS drift.
		inside := distance <= t.Radius
		if t.StopID == 0 {
			inside = distance <= t.Radius+20
		} else {
			inside = distance <= t.Radius-15
		}
		var done bool
		var at time.Time
		previousAt := st.CandidateAt
		st, done, at = st.advance(inside, t.StopID == 0, p.At)
		if st.Count == 0 {
			firstPositionID = sql.NullInt64{}
		}
		if st.Count == 1 && !st.CandidateAt.Equal(previousAt) {
			firstPositionID = sql.NullInt64{Int64: p.ID, Valid: true}
		}
		if done {
			kind := "chegada_cliente"
			if t.StopID == 0 {
				kind = "saida_deposito"
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO geofence_events(route_id,stop_order_id,vehicle_id,position_id,first_position_id,center_latitude,center_longitude,radius_m,kind,occurred_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(route_id,kind,stop_order_id) DO NOTHING`, t.RouteID, t.StopID, t.VehicleID, p.ID, nullableCursor(firstPositionID), t.Lat, t.Lng, int(t.Radius), kind, at)
			if err != nil {
				return err
			}
			if t.StopID == 0 {
				_, err = tx.ExecContext(ctx, `UPDATE routes SET actual_departure_at=$2 WHERE id=$1 AND actual_departure_at IS NULL`, t.RouteID, at)
				if err != nil {
					return err
				}
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE route_stops SET arrived_at=COALESCE(arrived_at,$3) WHERE route_id=$1 AND order_id=$2`, t.RouteID, t.StopID, at)
				if err != nil {
					return err
				}
			}
			break
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO geofence_progress(route_id,stop_order_id,last_position_id,last_gps_at,inside_seen,confirmation_count,candidate_at,candidate_position_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT(route_id,stop_order_id) DO UPDATE
SET last_position_id=EXCLUDED.last_position_id,last_gps_at=EXCLUDED.last_gps_at,inside_seen=EXCLUDED.inside_seen,
confirmation_count=EXCLUDED.confirmation_count,candidate_at=EXCLUDED.candidate_at,candidate_position_id=EXCLUDED.candidate_position_id`,
		t.RouteID, t.StopID, nullableCursor(cursor), nullTime(lastGPSTime), st.InsideSeen, st.Count, geofenceOptionalTime(st.CandidateAt), optionalPositionID(firstPositionID))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func optionalPositionID(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func nullableCursor(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return int64(0)
}
func geofenceOptionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
