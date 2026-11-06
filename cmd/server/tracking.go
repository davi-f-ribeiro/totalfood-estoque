package main

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"time"
)

// Acompanhamento público é somente leitura e nunca revela o código privado de
// confirmação, endereços, nomes de clientes, telefone, fotos ou local do motorista.
var publicTrackingTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type publicTrackingPage struct {
	Day    string
	Meal   string
	DueAt  string
	Status string
	ETA    string
}

func (a *app) handlePublicTracking(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; form-action 'none'")

	token := r.PathValue("token")
	if !publicTrackingTokenPattern.MatchString(token) {
		http.NotFound(w, r)
		return
	}

	var day, status, dueAt string
	var meal, eta sql.NullString
	err := a.db.QueryRowContext(r.Context(), `
SELECT o.day::text, o.status, o.meal,
       to_char(o.due_at AT TIME ZONE 'America/Fortaleza','HH24:MI'),
       (SELECT to_char(s.eta AT TIME ZONE 'America/Fortaleza','HH24:MI')
        FROM route_stops s JOIN routes rt ON rt.id=s.route_id
        WHERE s.order_id=o.id AND rt.status IN ('liberado','em_andamento')
        ORDER BY rt.id DESC LIMIT 1)
FROM delivery_orders o WHERE o.public_tracking_token=$1`, token).Scan(&day, &status, &meal, &dueAt, &eta)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("public tracking lookup failed: %v", err)
		http.Error(w, "Não foi possível consultar a entrega agora.", http.StatusInternalServerError)
		return
	}

	statusNames := map[string]string{
		"criado": "Pedido recebido", "programado": "Entrega programada",
		"despachado": "Pronta para transporte", "em_rota": "Em rota",
		"chegou": "Motorista chegou", "entregue": "Entregue",
		"falhou": "Entrega não concluída", "cancelado": "Entrega cancelada",
	}
	mealNames := map[string]string{"cafe": "Café", "almoco": "Almoço", "lanche": "Lanche", "jantar": "Jantar"}
	parsed, parseErr := time.Parse("2006-01-02", day)
	if parseErr == nil {
		day = parsed.Format("02/01/2006")
	}
	view := publicTrackingPage{Day: day, DueAt: dueAt, Status: statusNames[status], Meal: mealNames[meal.String]}
	if status == "liberado" || status == "em_rota" || status == "despachado" {
		view.ETA = eta.String
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := uiTemplates.ExecuteTemplate(w, "tracking.html", view); err != nil {
		log.Printf("public tracking template failed: %v", err)
	}
}
