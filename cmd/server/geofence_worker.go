package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const geofenceWorkerLock int64 = 71901421

func (a *app) runGeofenceWorker(ctx context.Context) error {
	if a.db.Stats().MaxOpenConnections > 0 && a.db.Stats().MaxOpenConnections < 2 {
		return errors.New("geofencing requer DB_MAX_OPEN_CONNS >= 2 (conexão para bloqueio e operações)")
	}

	if _, err := rastroCipher(); err != nil {
		return fmt.Errorf("RastroConnect não configurado: %w", err)
	}
	zone := strings.TrimSpace(os.Getenv("RASTROCONNECT_DEVICE_TZ"))
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return fmt.Errorf("RASTROCONNECT_DEVICE_TZ inválido: %w", err)
		}
	}
	interval := 60
	if s := os.Getenv("GEOFENCE_POLL_SECONDS"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 30 || n > 600 {
			return errors.New("GEOFENCE_POLL_SECONDS deve estar entre 30 e 600")
		}
		interval = n
	}
	log.Printf("geofence worker started interval=%ds timezone=%q", interval, zone)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for {
		cycleCtx, cancel := context.WithTimeout(ctx, time.Duration(interval-5)*time.Second)
		err := a.geofenceCycle(cycleCtx, zone)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("geofence worker cycle failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (a *app) geofenceCycle(ctx context.Context, zone string) error {
	// Session-level advisory lock on a dedicated connection prevents two workers
	// from polling/provider writes simultaneously. The connection stays open until
	// unlock; a process crash releases the lock automatically.
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", geofenceWorkerLock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := conn.ExecContext(cleanup, "SELECT pg_advisory_unlock($1)", geofenceWorkerLock)
		if err != nil {
			log.Printf("geofence lock release error: %v", err)
		}
	}()
	var company, login string
	var cipherText []byte
	err = a.db.QueryRowContext(ctx, `SELECT company,login,password_cipher FROM rastroconnect_settings WHERE id=1`).Scan(&company, &login, &cipherText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} // configuration not saved yet
	if err != nil {
		return err
	}
	password, err := rastroDecrypt(cipherText)
	if err != nil {
		return err
	}
	vehicles, skipped, err := rastroFleet(ctx, rastroBaseURL, company, login, password)
	if err != nil {
		return err
	}
	imported, linked, err := a.syncRastroVehicles(ctx, vehicles)
	if err != nil {
		return err
	}
	var stored, unusable int
	for _, v := range vehicles {
		if v.Latitude == nil || v.Longitude == nil {
			continue
		}
		gpsAt, valid := parseRastroGPSTime(v.SourceTime, zone)
		if !valid {
			unusable++
		}
		var id int64
		err = a.db.QueryRowContext(ctx, `SELECT vehicle_id FROM rastroconnect_vehicle_telemetry WHERE remote_id=$1`, v.RemoteID).Scan(&id)
		if err != nil {
			return err
		}
		var at any
		if valid {
			at = gpsAt
		}
		result, err := a.db.ExecContext(ctx, `INSERT INTO geofence_positions(vehicle_id,remote_id,latitude,longitude,gps_at,raw_device_time)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT DO NOTHING`, id, v.RemoteID, *v.Latitude, *v.Longitude, at, nullableRastroText(v.SourceTime))
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		stored += int(n)
	}
	if err = a.detectGeofence(ctx); err != nil {
		return err
	}
	log.Printf("geofence collected vehicles=%d new=%d linked=%d positions=%d timestamps_unknown=%d ignored=%d", len(vehicles), imported, linked, stored, unusable, len(skipped))
	return nil
}
