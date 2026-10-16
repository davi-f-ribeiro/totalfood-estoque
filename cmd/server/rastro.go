package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type rastroCredentials struct {
	Company  string `json:"company"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

// Encryption requires an independent, 32-byte random key. It is not derived
// from database credentials or the user's login password.
func rastroCipher() (cipher.AEAD, error) {
	raw := strings.TrimSpace(os.Getenv("RASTROCONNECT_KEY"))
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("configure RASTROCONNECT_KEY (64 caracteres hexadecimais) no servidor")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func rastroEncrypt(secret string) ([]byte, error) {
	aead, err := rastroCipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append([]byte{1}, aead.Seal(nonce, nonce, []byte(secret), []byte("rastroconnect/credentials"))...), nil
}
func rastroDecrypt(b []byte) (string, error) {
	aead, err := rastroCipher()
	if err != nil {
		return "", err
	}
	n := aead.NonceSize()
	if len(b) < n+1 || b[0] != 1 {
		return "", errors.New("formato de credencial inválido")
	}
	v, err := aead.Open(nil, b[1:1+n], b[1+n:], []byte("rastroconnect/credentials"))
	if err != nil {
		return "", errors.New("chave de credenciais inválida")
	}
	return string(v), nil
}

func (a *app) registerRastroHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/rastroconnect", a.requireAuth("users", "write", a.handleRastroSettings))
	mux.HandleFunc("PUT /api/admin/rastroconnect", a.requireAuth("users", "write", a.handleRastroSave))
	mux.HandleFunc("POST /api/admin/rastroconnect/sync", a.requireAuth("users", "write", a.handleRastroSync))
	mux.HandleFunc("GET /api/vehicles/telemetry", a.requireAuth("logistics", "read", a.handleRastroTelemetry))
}

func (a *app) handleRastroSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var company, login string
	var synced sql.NullTime
	err := a.db.QueryRowContext(r.Context(), `SELECT company,login,last_sync_at FROM rastroconnect_settings WHERE id=1`).Scan(&company, &login, &synced)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "key_ready": rastroKeyReady()})
		return
	}
	if err != nil {
		writeJSONError(w, 500, "Não foi possível carregar a configuração de rastreamento.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "company": company, "login": login, "password_saved": true, "key_ready": rastroKeyReady(), "last_sync_at": nullTime(synced)})
}
func rastroKeyReady() bool { _, err := rastroCipher(); return err == nil }
func nullTime(n sql.NullTime) any {
	if n.Valid {
		return n.Time
	}
	return nil
}

func (a *app) handleRastroSave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input rastroCredentials
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Company = strings.TrimSpace(input.Company)
	input.Login = strings.TrimSpace(input.Login)
	if input.Company == "" || input.Login == "" || len(input.Company) > 128 || len(input.Login) > 128 || len(input.Password) > 512 {
		writeJSONError(w, 400, "Informe empresa, usuário e senha válidos.")
		return
	}
	if !rastroKeyReady() {
		writeJSONError(w, 503, "Configure RASTROCONNECT_KEY no servidor antes de salvar as credenciais.")
		return
	}
	var encrypted []byte
	if input.Password == "" {
		err := a.db.QueryRowContext(r.Context(), `SELECT password_cipher FROM rastroconnect_settings WHERE id=1`).Scan(&encrypted)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, 400, "Informe a senha do rastreador.")
			return
		}
		if err != nil {
			writeJSONError(w, 500, "Não foi possível manter a senha salva.")
			return
		}
		if _, err = rastroDecrypt(encrypted); err != nil {
			writeJSONError(w, 503, "A chave do rastreador não corresponde à senha salva.")
			return
		}
	} else {
		var err error
		encrypted, err = rastroEncrypt(input.Password)
		if err != nil {
			writeJSONError(w, 503, "Não foi possível proteger a senha.")
			return
		}
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO rastroconnect_settings(id,company,login,password_cipher) VALUES (1,$1,$2,$3)
        ON CONFLICT(id) DO UPDATE SET company=excluded.company,login=excluded.login,password_cipher=excluded.password_cipher,last_sync_at=NULL`, input.Company, input.Login, encrypted)
	if err != nil {
		log.Printf("rastro settings persistence failed: %T", err)
		writeJSONError(w, 500, "Não foi possível salvar a configuração.")
		return
	}
	writeJSON(w, 200, map[string]any{"saved": true})
}
func (a *app) handleRastroSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var company, login string
	var encrypted []byte
	err := a.db.QueryRowContext(r.Context(), `SELECT company,login,password_cipher FROM rastroconnect_settings WHERE id=1`).Scan(&company, &login, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 400, "Salve as credenciais do rastreador primeiro.")
		return
	}
	if err != nil {
		writeJSONError(w, 500, "Não foi possível ler a configuração.")
		return
	}
	pass, err := rastroDecrypt(encrypted)
	if err != nil {
		writeJSONError(w, 503, "Não foi possível abrir a senha. Confira RASTROCONNECT_KEY.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	vehicles, err := rastroFleet(ctx, rastroBaseURL, company, login, pass)
	if err != nil {
		log.Printf("rastro sync fetch failed: %T", err)
		writeJSONError(w, 502, "Não foi possível consultar a frota no Rastro Connect. Confira o acesso e tente novamente.")
		return
	}
	imported, linked, err := a.syncRastroVehicles(r.Context(), vehicles)
	if err != nil {
		log.Printf("rastro sync database failed: %T", err)
		writeJSONError(w, 500, "Não foi possível conciliar a frota recebida.")
		return
	}
	writeJSON(w, 200, map[string]any{"received": len(vehicles), "imported": imported, "linked": linked, "pending_capacity": imported})
}

func (a *app) syncRastroVehicles(ctx context.Context, vehicles []rastroVehicle) (int, int, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	// Safe across multiple instances; short transaction, no remote I/O inside.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71901420)`); err != nil {
		return 0, 0, err
	}
	inserted, linked := 0, 0
	seenPlate := map[string]bool{}
	seenRemote := map[string]bool{}
	for _, v := range vehicles {
		if seenPlate[v.Plate] || seenRemote[v.RemoteID] {
			continue
		}
		seenPlate[v.Plate] = true
		seenRemote[v.RemoteID] = true
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO vehicles(plate,description,capacity_kg,active)
            VALUES ($1,$2,NULL,false) ON CONFLICT (plate) DO NOTHING RETURNING id`, v.Plate, v.Description).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `SELECT id FROM vehicles WHERE plate=$1`, v.Plate).Scan(&id)
			if err != nil {
				return 0, 0, err
			}
			linked++
		} else if err != nil {
			return 0, 0, err
		} else {
			inserted++
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO rastroconnect_vehicle_telemetry(vehicle_id,remote_id,equipment_id,latitude,longitude,speed_kmh,ignition,source_time)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
            ON CONFLICT(vehicle_id) DO UPDATE SET remote_id=excluded.remote_id,equipment_id=excluded.equipment_id,
                latitude=excluded.latitude,longitude=excluded.longitude,speed_kmh=excluded.speed_kmh,
                ignition=excluded.ignition,source_time=excluded.source_time,synced_at=now()`,
			id, v.RemoteID, nullableRastroText(v.EquipmentID), v.Latitude, v.Longitude, v.Speed, v.Ignition, nullableRastroText(v.SourceTime))
		if err != nil {
			return 0, 0, fmt.Errorf("telemetria da frota: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE rastroconnect_settings SET last_sync_at=now() WHERE id=1`); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return inserted, linked, nil
}
func nullableRastroText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (a *app) handleRastroTelemetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := a.db.QueryContext(r.Context(), `SELECT v.id,v.plate,t.remote_id,t.latitude::text,t.longitude::text,t.speed_kmh::text,t.ignition,t.source_time,t.synced_at
        FROM rastroconnect_vehicle_telemetry t JOIN vehicles v ON v.id=t.vehicle_id ORDER BY v.plate`)
	if err != nil {
		writeJSONError(w, 500, "Não foi possível consultar a telemetria.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var plate, remote string
		var lat, lng, speed, source sql.NullString
		var ignition sql.NullBool
		var synced time.Time
		if err = rows.Scan(&id, &plate, &remote, &lat, &lng, &speed, &ignition, &source, &synced); err != nil {
			writeJSONError(w, 500, "Não foi possível carregar a telemetria.")
			return
		}
		var ig any
		if ignition.Valid {
			ig = ignition.Bool
		}
		out = append(out, map[string]any{"vehicle_id": id, "plate": plate, "remote_id": remote, "latitude": nullText(lat), "longitude": nullText(lng), "speed_kmh": nullText(speed), "ignition": ig, "source_time": nullText(source), "synced_at": synced})
	}
	if err = rows.Err(); err != nil {
		writeJSONError(w, 500, "Não foi possível carregar a telemetria.")
		return
	}
	writeJSON(w, 200, out)
}
