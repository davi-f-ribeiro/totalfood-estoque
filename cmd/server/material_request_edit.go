package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
)

// Edição de requisição. O solicitante (ou a gerência) corrige um item pedido errado,
// a quantidade ou os dados do pedido, sem cancelar nem alterar direto no banco.
//
// Regras:
//   - só enquanto não foi recebida, encerrada ou cancelada;
//   - item que já teve entrega não sai do pedido, e a quantidade não fica abaixo do entregue;
//   - a data de produção não muda depois da primeira entrega (a saída já foi lançada com ela);
//   - item removido libera a separação; quantidade reduzida devolve ao saldo livre o que
//     a separação tiver acima do novo pedido (com a mesma folga de 20% para KG);
//   - nada aqui movimenta estoque.
//
// O corpo é o estado completo desejado. Reenviar o mesmo corpo depois de salvo não muda
// nada (responde 200); `version` impede que uma tela desatualizada sobrescreva a edição de outra pessoa.

type materialEditInput struct {
	Version        int64               `json:"version"`
	UnitName       string              `json:"unit_name"`
	DeliveryDate   string              `json:"delivery_date"`
	ProductionDate string              `json:"production_date"`
	Service        string              `json:"service"`
	Notes          string              `json:"notes"`
	Lines          []materialLineInput `json:"lines"`
}

type matEditLine struct {
	ID        int64
	ItemID    int64
	Name      string
	Unit      string
	Quantity  *big.Rat
	Reserved  *big.Rat // saldo separado (reservas ativas)
	Delivered *big.Rat
}

func matRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return new(big.Rat)
	}
	return r
}
func matCeil3(r *big.Rat) *big.Rat {
	n := new(big.Rat).Mul(r, big.NewRat(1000, 1))
	q, m := new(big.Int).QuoRem(n.Num(), n.Denom(), new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(q, big.NewInt(1000))
}
func matFmt(r *big.Rat) string {
	s := r.FloatString(3)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

var serviceNames = map[string]string{"cafe": "Café da manhã", "almoco": "Almoço", "lanche": "Lanche", "jantar": "Jantar"}

func (a *app) handleMaterialEdit(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	var in materialEditInput
	if !matDecode(w, r, &in) {
		return
	}
	req := materialCreateInput{UnitName: in.UnitName, DeliveryDate: in.DeliveryDate, ProductionDate: in.ProductionDate, Service: in.Service, Notes: in.Notes, Lines: in.Lines}
	if msg := matNormalizeRequest(&req); msg != "" {
		writeJSONError(w, 400, msg)
		return
	}
	if in.Version <= 0 {
		writeJSONError(w, 400, "Atualize a página antes de editar a requisição.")
		return
	}
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	manager, err := a.hasPermission(ctx, actor, "material_fulfillment", "write")
	if err != nil {
		a.matRespond(w, err)
		return
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, owner, _, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if actor != owner && !manager {
		a.matRespond(w, matErr(403, "Somente o solicitante ou gerente pode editar a requisição."))
		return
	}
	var unit, delivery, production, service, notes string
	var version int64
	var closed, anyDelivery bool
	err = tx.QueryRowContext(ctx, `SELECT unit_name,delivery_date::text,production_date::text,service,notes,version,closed_at IS NOT NULL,
  EXISTS(SELECT 1 FROM material_request_deliveries WHERE request_id=$1) FROM material_requests WHERE id=$1`, id).
		Scan(&unit, &delivery, &production, &service, &notes, &version, &closed, &anyDelivery)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if status == "CANCELADA" {
		a.matRespond(w, matConflict("Requisição cancelada não pode ser editada."))
		return
	}
	if closed || matReceiptDone(status) {
		a.matRespond(w, matConflict("Requisição já recebida não pode ser editada."))
		return
	}

	// Linhas atuais, com o que já foi separado e entregue de cada uma.
	current := map[int64]*matEditLine{}
	rows, err := tx.QueryContext(ctx, `SELECT l.id,l.item_id,i.name,i.unit_code,l.quantity::text,
  COALESCE((SELECT sum(res.remaining_quantity) FROM material_request_reservations res WHERE res.request_line_id=l.id AND res.status='ATIVA'),0)::text,
  COALESCE((SELECT sum(d.quantity) FROM material_request_deliveries d JOIN material_request_reservations res ON res.id=d.reservation_id WHERE res.request_line_id=l.id),0)::text
  FROM material_request_lines l JOIN inventory_items i ON i.id=l.item_id WHERE l.request_id=$1 ORDER BY l.id FOR UPDATE OF l`, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	var order []int64
	for rows.Next() {
		var l matEditLine
		var qty, reserved, delivered string
		if err = rows.Scan(&l.ID, &l.ItemID, &l.Name, &l.Unit, &qty, &reserved, &delivered); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		l.Quantity, l.Reserved, l.Delivered = matRat(qty), matRat(reserved), matRat(delivered)
		current[l.ItemID] = &l
		order = append(order, l.ItemID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}

	// O que mudou.
	var changes []string
	wanted := map[int64]*big.Rat{}
	for _, l := range req.Lines {
		wanted[l.ItemID] = matRat(l.Quantity)
	}
	var removed []*matEditLine
	var resized []*matEditLine
	for _, itemID := range order {
		l := current[itemID]
		q, keep := wanted[itemID]
		if !keep {
			removed = append(removed, l)
		} else if q.Cmp(l.Quantity) != 0 {
			resized = append(resized, l)
		}
	}
	var added []materialLineInput
	for _, l := range req.Lines {
		if current[l.ItemID] == nil {
			added = append(added, l)
		}
	}
	headerChanged := req.UnitName != unit || req.DeliveryDate != delivery || req.ProductionDate != production || req.Service != service || req.Notes != notes
	if !headerChanged && len(removed) == 0 && len(resized) == 0 && len(added) == 0 {
		// Nada a mudar: também é o caso do reenvio de uma edição já salva.
		writeJSON(w, 200, map[string]any{"id": id, "status": status, "version": version, "changed": false})
		return
	}
	if in.Version != version {
		writeConcurrentEditError(w)
		return
	}
	if req.ProductionDate != production && anyDelivery {
		a.matRespond(w, matConflict("Já houve entrega com produção em "+brDate(production)+". A data de produção não pode mais mudar."))
		return
	}

	// Remoção: só de item sem entrega. A separação (reserva) do item deixa de existir.
	for _, l := range removed {
		if l.Delivered.Sign() > 0 {
			a.matRespond(w, matConflict(fmt.Sprintf("%s já teve entrega de %s %s e não pode sair da requisição. Reduza a quantidade até o entregue.", l.Name, matFmt(l.Delivered), l.Unit)))
			return
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM material_request_reservations WHERE request_line_id=$1", l.ID); err != nil {
			a.matRespond(w, err)
			return
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM material_request_lines WHERE id=$1", l.ID); err != nil {
			a.matRespond(w, err)
			return
		}
		c := fmt.Sprintf("removido %s (%s %s)", l.Name, matFmt(l.Quantity), l.Unit)
		if l.Reserved.Sign() > 0 {
			c += fmt.Sprintf(", separação de %s %s liberada", matFmt(l.Reserved), l.Unit)
		}
		changes = append(changes, c)
	}

	// Quantidade: não fica abaixo do entregue; a separação acima do novo pedido volta ao saldo livre.
	for _, l := range resized {
		q := wanted[l.ItemID]
		if q.Cmp(l.Delivered) < 0 {
			a.matRespond(w, matConflict(fmt.Sprintf("%s: já foram entregues %s %s. A quantidade não pode ficar abaixo disso.", l.Name, matFmt(l.Delivered), l.Unit)))
			return
		}
		if _, err = tx.ExecContext(ctx, "UPDATE material_request_lines SET quantity=$2::numeric WHERE id=$1", l.ID, q.FloatString(3)); err != nil {
			a.matRespond(w, err)
			return
		}
		c := fmt.Sprintf("%s: %s → %s %s", l.Name, matFmt(l.Quantity), matFmt(q), l.Unit)
		limit := new(big.Rat).Set(q)
		if l.Unit == "KG" {
			limit.Mul(limit, matRat(matWeightTolerance))
		}
		excess := new(big.Rat).Add(l.Reserved, l.Delivered)
		excess.Sub(excess, limit)
		if excess.Sign() > 0 {
			excess = matCeil3(excess) // o banco guarda 3 casas; arredonda para cima para caber no limite
			freed, err := matTrimReservations(r, tx, l.ID, excess, actor)
			if err != nil {
				a.matRespond(w, err)
				return
			}
			c += fmt.Sprintf(", %s %s da separação liberados", matFmt(freed), l.Unit)
		}
		changes = append(changes, c)
	}

	for _, l := range added {
		var name, unitCode string
		var active bool
		err = tx.QueryRowContext(ctx, "SELECT name,unit_code,active FROM inventory_items WHERE id=$1", l.ItemID).Scan(&name, &unitCode, &active)
		if errors.Is(err, sql.ErrNoRows) {
			a.matRespond(w, matBad("Item não encontrado."))
			return
		}
		if err != nil {
			a.matRespond(w, err)
			return
		}
		if !active {
			a.matRespond(w, matBad(name+" está inativo e não pode ser incluído."))
			return
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO material_request_lines(request_id,item_id,quantity) VALUES($1,$2,$3::numeric)", id, l.ItemID, l.Quantity); err != nil {
			a.matRespond(w, err)
			return
		}
		changes = append(changes, fmt.Sprintf("incluído %s (%s %s)", name, matFmt(matRat(l.Quantity)), unitCode))
	}

	if req.UnitName != unit {
		changes = append(changes, "unidade: "+unit+" → "+req.UnitName)
	}
	if req.ProductionDate != production {
		changes = append(changes, "produção: "+brDate(production)+" → "+brDate(req.ProductionDate))
	}
	if req.DeliveryDate != delivery {
		changes = append(changes, "entrega desejada: "+brDate(delivery)+" → "+brDate(req.DeliveryDate))
	}
	if req.Service != service {
		changes = append(changes, "serviço: "+serviceNames[service]+" → "+serviceNames[req.Service])
	}
	if req.Notes != notes {
		changes = append(changes, "observação alterada")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE material_requests SET unit_name=$2,delivery_date=$3::date,production_date=$4::date,service=$5,notes=$6,
  version=version+1,updated_at=now() WHERE id=$1`, id, req.UnitName, req.DeliveryDate, req.ProductionDate, req.Service, req.Notes); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matEvent(ctx, tx, id, actor, "EDITADA", strings.Join(changes, "; ")); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matRefresh(ctx, tx, id); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.QueryRowContext(ctx, "SELECT status,version FROM material_requests WHERE id=$1", id).Scan(&status, &version); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": status, "version": version, "changed": true, "changes": changes})
}

// matTrimReservations tira `excess` do saldo separado da linha, das separações mais novas
// para as mais antigas. Separação que zera fica LIBERADA (ou CONCLUIDA, se já teve entrega).
func matTrimReservations(r *http.Request, tx *sql.Tx, lineID int64, excess *big.Rat, actor int64) (*big.Rat, error) {
	ctx := r.Context()
	rows, err := tx.QueryContext(ctx, `SELECT res.id,res.remaining_quantity::text,EXISTS(SELECT 1 FROM material_request_deliveries d WHERE d.reservation_id=res.id)
  FROM material_request_reservations res WHERE res.request_line_id=$1 AND res.status='ATIVA' AND res.remaining_quantity>0 ORDER BY res.id DESC FOR UPDATE`, lineID)
	if err != nil {
		return nil, err
	}
	type held struct {
		id        int64
		remaining *big.Rat
		delivered bool
	}
	var list []held
	for rows.Next() {
		var h held
		var rem string
		if err = rows.Scan(&h.id, &rem, &h.delivered); err != nil {
			rows.Close()
			return nil, err
		}
		h.remaining = matRat(rem)
		list = append(list, h)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	left := new(big.Rat).Set(excess)
	freed := new(big.Rat)
	for _, h := range list {
		if left.Sign() <= 0 {
			break
		}
		take := new(big.Rat).Set(h.remaining)
		if left.Cmp(take) < 0 {
			take.Set(left)
		}
		if take.Cmp(h.remaining) == 0 {
			if h.delivered {
				_, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=0,status='CONCLUIDA' WHERE id=$1", h.id)
			} else {
				_, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=0,status='LIBERADA',released_by=$2,released_at=now() WHERE id=$1", h.id, actor)
			}
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=remaining_quantity-$2::numeric WHERE id=$1", h.id, take.FloatString(3))
		}
		if err != nil {
			return nil, err
		}
		left.Sub(left, take)
		freed.Add(freed, take)
	}
	return freed, nil
}

func brDate(s string) string {
	p := strings.Split(s, "-")
	if len(p) != 3 {
		return s
	}
	return p[2] + "/" + p[1] + "/" + p[0]
}
