package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Cadastros de logística (fase 2 da cadeia de suprimentos): locais de entrega e
// coleta, veículos e motoristas. Nada aqui mexe em estoque; a expedição usa
// estes cadastros numa fase seguinte.

var (
	platePattern       = regexp.MustCompile(`^[A-Z]{3}[0-9][A-Z0-9][0-9]{2}$`)
	licenseNumberRegex = regexp.MustCompile(`^[0-9]{11}$`)
	clockPattern       = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	capacityPattern    = regexp.MustCompile(`^(0|[1-9][0-9]{0,7})(\.[0-9]{1,2})?$`)
	isoDatePattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	placeKinds         = map[string]bool{"cliente": true, "fornecedor": true, "deposito": true, "outro": true}
	licenseCategories  = map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true, "AB": true, "AC": true, "AD": true, "AE": true}
)

type placeRequest struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	CustomerID  int64  `json:"customer_id,omitempty"`
	WarehouseID int64  `json:"warehouse_id,omitempty"`
	Address     string `json:"address"`
	City        string `json:"city"`
	Latitude    string `json:"latitude"`
	Longitude   string `json:"longitude"`
	WindowStart string `json:"window_start"`
	WindowEnd   string `json:"window_end"`
	Contact     string `json:"contact"`
	AccessNotes string `json:"access_notes"`
	Active      *bool  `json:"active"`
	Version     int64  `json:"version,omitempty"`
}

type vehicleRequest struct {
	Plate         string `json:"plate"`
	Description   string `json:"description"`
	AssetID       int64  `json:"asset_id,omitempty"`
	CapacityKG    string `json:"capacity_kg"`
	CapacityBoxes int64  `json:"capacity_boxes,omitempty"`
	Refrigerated  bool   `json:"refrigerated"`
	Ownership     string `json:"ownership,omitempty"`
	Active        *bool  `json:"active"`
	Version       int64  `json:"version,omitempty"`
}

type driverRequest struct {
	Name             string `json:"name"`
	Phone            string `json:"phone"`
	LicenseNumber    string `json:"license_number"`
	LicenseCategory  string `json:"license_category"`
	LicenseExpiresAt string `json:"license_expires_at"`
	UserID           int64  `json:"user_id,omitempty"`
	DefaultVehicleID int64  `json:"default_vehicle_id,omitempty"`
	Active           *bool  `json:"active"`
	Version          int64  `json:"version,omitempty"`
}

func (a *app) registerLogisticsHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /logistics", a.uiPage("logistics", "Locais e frota", "logistics", "read"))
	mux.HandleFunc("GET /api/logistics/options", a.requireAuth("logistics", "write", a.handleLogisticsOptions))
	mux.HandleFunc("GET /api/places", a.requireAuth("logistics", "read", a.handlePlaces))
	mux.HandleFunc("GET /api/places/coverage", a.requireAuth("logistics", "read", a.handlePlaceCoverage))
	mux.HandleFunc("POST /api/places", a.requireAuth("logistics", "write", a.handleSavePlace))
	mux.HandleFunc("PATCH /api/places/{id}", a.requireAuth("logistics", "write", a.handleSavePlace))
	mux.HandleFunc("GET /api/vehicles", a.requireAuth("logistics", "read", a.handleVehicles))
	mux.HandleFunc("POST /api/vehicles", a.requireAuth("logistics", "write", a.handleSaveVehicle))
	mux.HandleFunc("PATCH /api/vehicles/{id}", a.requireAuth("logistics", "write", a.handleSaveVehicle))
	mux.HandleFunc("GET /api/drivers", a.requireAuth("logistics", "read", a.handleDrivers))
	mux.HandleFunc("POST /api/drivers", a.requireAuth("logistics", "write", a.handleSaveDriver))
	mux.HandleFunc("PATCH /api/drivers/{id}", a.requireAuth("logistics", "write", a.handleSaveDriver))
}

func logisticsFail(w http.ResponseWriter, status int, msg, logPrefix string, err error) {
	log.Printf("%s error=%v", logPrefix, err)
	writeJSONError(w, status, msg)
}

func optionalText(s string) any {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return nil
}

func optionalID(id int64) any {
	if id > 0 {
		return id
	}
	return nil
}

func activeOrDefault(v *bool) bool {
	return v == nil || *v
}

// parseCoordinate aceita vírgula ou ponto e devolve o valor normalizado com no
// máximo seis casas, o mesmo limite de numeric(9,6).
func parseCoordinate(raw string, limit float64) (string, bool) {
	v := normalizeDecimalInput(raw)
	if v == "" {
		return "", true
	}
	if i := strings.IndexByte(v, '.'); i >= 0 && len(v)-i-1 > 6 {
		return "", false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < -limit || f > limit {
		return "", false
	}
	return v, true
}

func normalizePlaceRequest(req *placeRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	req.Kind = strings.TrimSpace(req.Kind)
	req.Address = strings.TrimSpace(req.Address)
	req.WindowStart = strings.TrimSpace(req.WindowStart)
	req.WindowEnd = strings.TrimSpace(req.WindowEnd)
	switch {
	case req.Name == "":
		return "Digite o nome do local."
	case !placeKinds[req.Kind]:
		return "Escolha o tipo do local."
	case req.Kind == "cliente" && req.CustomerID <= 0:
		return "Escolha o cliente deste local."
	case req.Kind == "deposito" && req.WarehouseID <= 0:
		return "Escolha o depósito deste local."
	case req.Address == "":
		return "Digite o endereço do local."
	}
	if req.Kind != "cliente" {
		req.CustomerID = 0
	}
	if req.Kind != "deposito" {
		req.WarehouseID = 0
	}
	lat, okLat := parseCoordinate(req.Latitude, 90)
	lng, okLng := parseCoordinate(req.Longitude, 180)
	if !okLat {
		return "Latitude inválida. Use um número entre -90 e 90, com até 6 casas."
	}
	if !okLng {
		return "Longitude inválida. Use um número entre -180 e 180, com até 6 casas."
	}
	if (lat == "") != (lng == "") {
		return "Informe latitude e longitude juntas, ou deixe as duas em branco."
	}
	req.Latitude, req.Longitude = lat, lng
	if (req.WindowStart == "") != (req.WindowEnd == "") {
		return "Informe o início e o fim da janela de recebimento, ou deixe os dois em branco."
	}
	if req.WindowStart != "" {
		if !clockPattern.MatchString(req.WindowStart) || !clockPattern.MatchString(req.WindowEnd) {
			return "Horário inválido. Use o formato 07:30."
		}
		if req.WindowStart >= req.WindowEnd {
			return "O fim da janela de recebimento precisa ser depois do início."
		}
	}
	return ""
}

func writePlaceError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	msg := err.Error()
	switch {
	case strings.Contains(msg, "idx_places_normalized_name_unique"):
		writeJSONError(w, http.StatusConflict, "Já existe um local com este nome.")
	case strings.Contains(msg, "places_customer_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Cliente não encontrado. Atualize a página e escolha de novo.")
	case strings.Contains(msg, "places_warehouse_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Depósito não encontrado. Atualize a página e escolha de novo.")
	default:
		writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o local.")
	}
}

const placeSelect = `
SELECT p.id,p.name,p.kind,p.customer_id,c.name,p.warehouse_id,wh.name,p.address,p.city,
       p.latitude::text,p.longitude::text,to_char(p.window_start,'HH24:MI'),to_char(p.window_end,'HH24:MI'),
       p.contact,p.access_notes,p.active,p.version
FROM places p
LEFT JOIN customers c ON c.id=p.customer_id
LEFT JOIN warehouses wh ON wh.id=p.warehouse_id
ORDER BY p.active DESC,lower(p.name),p.id`

func (a *app) handlePlaces(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), placeSelect)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os locais.", "places list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var name, kind, address string
		var customerID, warehouseID sql.NullInt64
		var customerName, warehouseName, city, lat, lng, ws, we, contact, notes sql.NullString
		var active bool
		if err := rows.Scan(&id, &name, &kind, &customerID, &customerName, &warehouseID, &warehouseName, &address, &city,
			&lat, &lng, &ws, &we, &contact, &notes, &active, &version); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os locais.", "places scan failed", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "kind": kind,
			"customer_id": nullInt(customerID), "customer_name": nullText(customerName),
			"warehouse_id": nullInt(warehouseID), "warehouse_name": nullText(warehouseName),
			"address": address, "city": nullText(city),
			"latitude": nullText(lat), "longitude": nullText(lng),
			"window_start": nullText(ws), "window_end": nullText(we),
			"contact": nullText(contact), "access_notes": nullText(notes),
			"active": active, "version": version,
		})
	}
	if err := rows.Err(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os locais.", "places rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePlaceCoverage lista os clientes ativos que ainda não têm um local de
// entrega ativo com coordenada. Sem coordenada não há rota na fase de expedição.
func (a *app) handlePlaceCoverage(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `
SELECT c.id,c.name
FROM customers c
WHERE c.active
  AND NOT EXISTS (
      SELECT 1 FROM places p
      WHERE p.customer_id=c.id AND p.active AND p.latitude IS NOT NULL)
ORDER BY lower(c.name),c.id`)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível verificar os clientes sem local.", "place coverage failed", err)
		return
	}
	defer rows.Close()
	missing := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível verificar os clientes sem local.", "place coverage scan failed", err)
			return
		}
		missing = append(missing, map[string]any{"id": id, "name": name})
	}
	if err := rows.Err(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível verificar os clientes sem local.", "place coverage rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"customers_without_place": missing})
}

// saveVersioned roda o INSERT (sem id no caminho) ou o UPDATE com controle de
// versão (PATCH /{id}) e responde no formato dos outros cadastros.
func (a *app) saveVersioned(w http.ResponseWriter, r *http.Request, table, notFound string, version int64,
	insert func(ctx context.Context) (int64, error),
	update func(ctx context.Context, id int64) (int64, error),
	fail func(w http.ResponseWriter, err error, logPrefix string)) {
	ctx := r.Context()
	if r.PathValue("id") == "" {
		id, err := insert(ctx)
		if err != nil {
			fail(w, err, table+" create failed")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": int64(1)})
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Atualize a página e tente de novo.")
		return
	}
	newVersion, err := update(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if qerr := a.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=$1)`, id).Scan(&exists); qerr != nil {
			fail(w, qerr, table+" lookup failed")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, notFound)
			return
		}
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		fail(w, err, table+" update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": newVersion})
}

func (a *app) handleSavePlace(w http.ResponseWriter, r *http.Request) {
	var req placeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if problem := normalizePlaceRequest(&req); problem != "" {
		writeJSONError(w, http.StatusBadRequest, problem)
		return
	}
	args := []any{req.Name, req.Kind, optionalID(req.CustomerID), optionalID(req.WarehouseID), req.Address, optionalText(req.City),
		optionalText(req.Latitude), optionalText(req.Longitude), optionalText(req.WindowStart), optionalText(req.WindowEnd),
		optionalText(req.Contact), optionalText(req.AccessNotes), activeOrDefault(req.Active)}
	a.saveVersioned(w, r, "places", "Local não encontrado.", req.Version,
		func(ctx context.Context) (int64, error) {
			var id int64
			err := a.db.QueryRowContext(ctx, `
INSERT INTO places (name,kind,customer_id,warehouse_id,address,city,latitude,longitude,window_start,window_end,contact,access_notes,active)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`, args...).Scan(&id)
			return id, err
		},
		func(ctx context.Context, id int64) (int64, error) {
			var version int64
			err := a.db.QueryRowContext(ctx, `
UPDATE places SET name=$1,kind=$2,customer_id=$3,warehouse_id=$4,address=$5,city=$6,latitude=$7,longitude=$8,
       window_start=$9,window_end=$10,contact=$11,access_notes=$12,active=$13,version=version+1
WHERE id=$14 AND version=$15 RETURNING version`, append(args, id, req.Version)...).Scan(&version)
			return version, err
		},
		writePlaceError)
}

func normalizeVehicleRequest(req *vehicleRequest) string {
	req.Plate = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(req.Plate))
	req.Description = strings.TrimSpace(req.Description)
	req.CapacityKG = normalizeDecimalInput(req.CapacityKG)
	// Frota mista: a maior parte é própria, alguns veículos são alugados.
	if req.Ownership = strings.TrimSpace(req.Ownership); req.Ownership == "" {
		req.Ownership = "propria"
	}
	switch {
	case req.Ownership != "propria" && req.Ownership != "alugada":
		return "Escolha se o veículo é próprio ou alugado."
	case !platePattern.MatchString(req.Plate):
		return "Placa inválida. Use o formato ABC1234 ou ABC1D23."
	case req.Description == "":
		return "Descreva o veículo (ex.: Fiorino refrigerada)."
	case !capacityPattern.MatchString(req.CapacityKG) || strings.Trim(req.CapacityKG, "0.") == "":
		return "Informe a capacidade em kg, maior que zero."
	case req.CapacityBoxes < 0:
		return "A quantidade de caixas térmicas não pode ser negativa."
	}
	return ""
}

func writeVehicleError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	msg := err.Error()
	switch {
	case strings.Contains(msg, "idx_vehicles_plate_unique"):
		writeJSONError(w, http.StatusConflict, "Já existe um veículo com esta placa.")
	case strings.Contains(msg, "vehicles_asset_id_key"):
		writeJSONError(w, http.StatusConflict, "Este bem do patrimônio já está ligado a outro veículo.")
	case strings.Contains(msg, "vehicles_asset_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Bem do patrimônio não encontrado. Atualize a página e escolha de novo.")
	default:
		writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o veículo.")
	}
}

func (a *app) handleVehicles(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `
SELECT v.id,v.plate,v.description,v.asset_id,s.tag,v.capacity_kg::text,v.capacity_boxes,v.refrigerated,v.ownership,v.active,v.version
FROM vehicles v
LEFT JOIN assets s ON s.id=v.asset_id
ORDER BY v.active DESC,v.plate,v.id`)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os veículos.", "vehicles list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var plate, description, ownership string
		var capacity sql.NullString
		var assetID, boxes sql.NullInt64
		var assetTag sql.NullString
		var refrigerated, active bool
		if err := rows.Scan(&id, &plate, &description, &assetID, &assetTag, &capacity, &boxes, &refrigerated, &ownership, &active, &version); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os veículos.", "vehicles scan failed", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "plate": plate, "description": description,
			"asset_id": nullInt(assetID), "asset_tag": nullText(assetTag),
			"capacity_kg": nullText(capacity), "capacity_boxes": nullInt(boxes),
			"refrigerated": refrigerated, "ownership": ownership, "active": active, "version": version,
		})
	}
	if err := rows.Err(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os veículos.", "vehicles rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleSaveVehicle(w http.ResponseWriter, r *http.Request) {
	var req vehicleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if problem := normalizeVehicleRequest(&req); problem != "" {
		writeJSONError(w, http.StatusBadRequest, problem)
		return
	}
	args := []any{req.Plate, req.Description, optionalID(req.AssetID), req.CapacityKG, optionalID(req.CapacityBoxes), req.Refrigerated, activeOrDefault(req.Active), req.Ownership}
	a.saveVersioned(w, r, "vehicles", "Veículo não encontrado.", req.Version,
		func(ctx context.Context) (int64, error) {
			var id int64
			err := a.db.QueryRowContext(ctx, `
INSERT INTO vehicles (plate,description,asset_id,capacity_kg,capacity_boxes,refrigerated,active,ownership)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, args...).Scan(&id)
			return id, err
		},
		func(ctx context.Context, id int64) (int64, error) {
			var version int64
			err := a.db.QueryRowContext(ctx, `
UPDATE vehicles SET plate=$1,description=$2,asset_id=$3,capacity_kg=$4,capacity_boxes=$5,refrigerated=$6,active=$7,ownership=$8,version=version+1
WHERE id=$9 AND version=$10 RETURNING version`, append(args, id, req.Version)...).Scan(&version)
			return version, err
		},
		writeVehicleError)
}

func normalizeDriverRequest(req *driverRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	req.LicenseNumber = strings.TrimSpace(req.LicenseNumber)
	req.LicenseCategory = strings.ToUpper(strings.TrimSpace(req.LicenseCategory))
	req.LicenseExpiresAt = strings.TrimSpace(req.LicenseExpiresAt)
	switch {
	case req.Name == "":
		return "Digite o nome do motorista."
	case req.LicenseNumber != "" && !licenseNumberRegex.MatchString(req.LicenseNumber):
		return "Número da CNH inválido. Use os 11 dígitos, sem pontos."
	case req.LicenseCategory != "" && !licenseCategories[req.LicenseCategory]:
		return "Categoria da CNH inválida."
	case req.LicenseExpiresAt != "" && !isoDatePattern.MatchString(req.LicenseExpiresAt):
		return "Data de validade da CNH inválida."
	}
	return ""
}

func writeDriverError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	msg := err.Error()
	switch {
	case strings.Contains(msg, "idx_drivers_normalized_name_unique"):
		writeJSONError(w, http.StatusConflict, "Já existe um motorista com este nome.")
	case strings.Contains(msg, "idx_drivers_license_number_unique"):
		writeJSONError(w, http.StatusConflict, "Esta CNH já está cadastrada para outro motorista.")
	case strings.Contains(msg, "drivers_user_id_key"):
		writeJSONError(w, http.StatusConflict, "Este usuário já está ligado a outro motorista.")
	case strings.Contains(msg, "drivers_user_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Usuário não encontrado. Atualize a página e escolha de novo.")
	case strings.Contains(msg, "drivers_default_vehicle_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Veículo não encontrado. Atualize a página e escolha de novo.")
	case strings.Contains(msg, "date/time field value out of range"), strings.Contains(msg, "invalid input syntax for type date"):
		writeJSONError(w, http.StatusBadRequest, "Data de validade da CNH inválida.")
	default:
		writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o motorista.")
	}
}

func (a *app) handleDrivers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `
SELECT d.id,d.name,d.phone,d.license_number,d.license_category,d.license_expires_at::text,
       d.license_expires_at < current_date,d.user_id,u.login,d.default_vehicle_id,v.plate,d.active,d.version
FROM drivers d
LEFT JOIN users u ON u.id=d.user_id
LEFT JOIN vehicles v ON v.id=d.default_vehicle_id
ORDER BY d.active DESC,lower(d.name),d.id`)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os motoristas.", "drivers list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var name string
		var phone, license, category, expires, login, plate sql.NullString
		var expired sql.NullBool
		var userID, vehicleID sql.NullInt64
		var active bool
		if err := rows.Scan(&id, &name, &phone, &license, &category, &expires, &expired, &userID, &login, &vehicleID, &plate, &active, &version); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os motoristas.", "drivers scan failed", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "phone": nullText(phone),
			"license_number": nullText(license), "license_category": nullText(category),
			"license_expires_at": nullText(expires), "license_expired": expired.Valid && expired.Bool,
			"user_id": nullInt(userID), "user_login": nullText(login),
			"default_vehicle_id": nullInt(vehicleID), "default_vehicle_plate": nullText(plate),
			"active": active, "version": version,
		})
	}
	if err := rows.Err(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar os motoristas.", "drivers rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleSaveDriver(w http.ResponseWriter, r *http.Request) {
	var req driverRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if problem := normalizeDriverRequest(&req); problem != "" {
		writeJSONError(w, http.StatusBadRequest, problem)
		return
	}
	args := []any{req.Name, optionalText(req.Phone), optionalText(req.LicenseNumber), optionalText(req.LicenseCategory),
		optionalText(req.LicenseExpiresAt), optionalID(req.UserID), optionalID(req.DefaultVehicleID), activeOrDefault(req.Active)}
	a.saveVersioned(w, r, "drivers", "Motorista não encontrado.", req.Version,
		func(ctx context.Context) (int64, error) {
			var id int64
			err := a.db.QueryRowContext(ctx, `
INSERT INTO drivers (name,phone,license_number,license_category,license_expires_at,user_id,default_vehicle_id,active)
VALUES ($1,$2,$3,$4,$5::date,$6,$7,$8) RETURNING id`, args...).Scan(&id)
			return id, err
		},
		func(ctx context.Context, id int64) (int64, error) {
			var version int64
			err := a.db.QueryRowContext(ctx, `
UPDATE drivers SET name=$1,phone=$2,license_number=$3,license_category=$4,license_expires_at=$5::date,
       user_id=$6,default_vehicle_id=$7,active=$8,version=version+1
WHERE id=$9 AND version=$10 RETURNING version`, append(args, id, req.Version)...).Scan(&version)
			return version, err
		},
		writeDriverError)
}

// handleLogisticsOptions alimenta os formulários: clientes, depósitos, bens do
// patrimônio e usuários. Exige escrita porque lista logins de usuários, que
// quem só consulta locais e frota não precisa ver.
func (a *app) handleLogisticsOptions(w http.ResponseWriter, r *http.Request) {
	queries := []struct{ key, sql string }{
		{"customers", `SELECT id,name FROM customers WHERE active ORDER BY lower(name),id`},
		{"warehouses", `SELECT id,name FROM warehouses WHERE active ORDER BY name,id`},
		{"assets", `SELECT s.id,s.tag||' · '||t.name FROM assets s JOIN asset_types t ON t.id=s.asset_type_id WHERE s.status<>'BAIXADO' ORDER BY s.tag,s.id`},
		{"users", `SELECT id,login FROM users WHERE active ORDER BY login,id`},
	}
	out := map[string]any{}
	for _, q := range queries {
		list, err := simpleRows(r, a.db, q.sql)
		if err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as opções.", "logistics options "+q.key+" failed", err)
			return
		}
		out[q.key] = list
	}
	writeJSON(w, http.StatusOK, out)
}
