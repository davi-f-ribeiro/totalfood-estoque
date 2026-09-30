package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func secondInventoryUser(t *testing.T, e inventoryTestEnv, label string) int64 {
	t.Helper()
	var profileID, userID int64
	if err := e.db.QueryRow(`SELECT id FROM profiles WHERE name='Diretor'`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`
INSERT INTO users (login,password_hash,profile_id,active,must_change_password)
VALUES ($1,'test',$2,true,false) RETURNING id`,
		"inventory-terminal-"+label+"-"+t.Name(), profileID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func oneReviewInventory(t *testing.T, e inventoryTestEnv, label, snapshot, first string) (inventorySelection, int64) {
	t.Helper()
	ctx := context.Background()
	wh := e.warehouse(t, "terminal-"+label, true)
	sku := e.sku(t, "terminal-"+label+"-sku", true, true)
	e.movementDecimal(t, sku, wh, "IN", snapshot)
	inv, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	countInventoryToReview(t, e, inv, map[int64]string{sku: first})
	return inv, sku
}

func TestInventoryCompleteRulesAndAudit(t *testing.T) {
	t.Run("direct match", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, _ := oneReviewInventory(t, e, "direct-match", "10.000", "10.000")
		out, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, "")
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "COMPLETED" || out.CompletedBy == "" || out.CompletedAt == nil {
			t.Fatalf("completion audit=%+v", out)
		}
		if out.CompletionNote != "" {
			t.Fatalf("unexpected note=%q", out.CompletionNote)
		}
		if len(out.Lines) != 1 || out.Lines[0].State == nil || *out.Lines[0].State != "MATCH" {
			t.Fatalf("history=%+v", out.Lines)
		}
	})

	t.Run("match after recount", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, sku := oneReviewInventory(t, e, "recount-match", "10.000", "8.000")
		review, err := e.a.getInventoryReviewView(context.Background(), inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		line := reviewBySKU(t, review)[sku]
		if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, sku, "10.000", line.Version, e.userID); err != nil {
			t.Fatal(err)
		}
		out, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, "")
		if err != nil {
			t.Fatal(err)
		}
		if out.Lines[0].State == nil || *out.Lines[0].State != "MATCH" {
			t.Fatalf("final state=%+v", out.Lines[0])
		}
		if out.Lines[0].FirstQuantity == nil || *out.Lines[0].FirstQuantity != "8.000" ||
			out.Lines[0].RecountQuantity == nil || *out.Lines[0].RecountQuantity != "10.000" {
			t.Fatalf("observations not preserved: %+v", out.Lines[0])
		}
	})

	t.Run("confirmed divergence requires note", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, sku := oneReviewInventory(t, e, "recount-divergent", "10.000", "8.000")
		review, _ := e.a.getInventoryReviewView(context.Background(), inv.ID)
		line := reviewBySKU(t, review)[sku]
		if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, sku, "9.000", line.Version, e.userID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, ""); !errors.Is(err, errInventoryNoteRequired) {
			t.Fatalf("empty divergent note error=%v", err)
		}
		out, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, "Divergência física confirmada na recontagem.")
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "COMPLETED" || out.CompletionNote == "" {
			t.Fatalf("completion=%+v", out)
		}
		if out.Lines[0].State == nil || *out.Lines[0].State != "DIVERGENT" ||
			out.Lines[0].Difference == nil || *out.Lines[0].Difference != "-1.000" {
			t.Fatalf("divergence was not preserved: %+v", out.Lines[0])
		}
	})

	t.Run("unrecounted divergence blocked", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, _ := oneReviewInventory(t, e, "unrecounted", "10.000", "8.000")
		if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, "Tentativa prematura."); !errors.Is(err, errInventoryCannotComplete) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("open and counting blocked", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		_, inv, _ := setupTotalCounting(t, e, 2, "10.000")
		if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, ""); !errors.Is(err, errInventoryCannotComplete) {
			t.Fatalf("OPEN error=%v", err)
		}
		view, _ := e.a.getInventoryCountingView(context.Background(), inv.ID)
		if _, _, err := e.a.saveInventoryCount(context.Background(), inv.ID, view.Lines[0].SKUID, "10.000", view.Lines[0].Version, e.userID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, ""); !errors.Is(err, errInventoryCannotComplete) {
			t.Fatalf("COUNTING error=%v", err)
		}
	})
}

func TestInventoryCancelStatesReasonAuditAndPreservation(t *testing.T) {
	t.Run("open", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		_, inv, _ := setupTotalCounting(t, e, 2, "10.000")
		out, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Contagem cancelada por decisão operacional.")
		if err != nil {
			t.Fatal(err)
		}
		if out.Status != "CANCELLED" || out.CancelledBy == "" || out.CancelledAt == nil || out.CancellationReason == "" {
			t.Fatalf("cancel audit=%+v", out)
		}
		if len(out.Lines) != 2 {
			t.Fatalf("selection history lost: %+v", out.Lines)
		}
	})
	t.Run("counting", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		_, inv, _ := setupTotalCounting(t, e, 2, "10.000")
		view, _ := e.a.getInventoryCountingView(context.Background(), inv.ID)
		line := view.Lines[0]
		if _, _, err := e.a.saveInventoryCount(context.Background(), inv.ID, line.SKUID, "9.000", line.Version, e.userID); err != nil {
			t.Fatal(err)
		}
		out, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Contagem parcial encerrada formalmente.")
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, h := range out.Lines {
			if h.SKUID == line.SKUID {
				found = h.FirstQuantity != nil && *h.FirstQuantity == "9.000" && h.FirstCountedBy != "" && h.FirstCountedAt != nil
			}
		}
		if !found {
			t.Fatalf("partial count audit lost: %+v", out.Lines)
		}
	})
	t.Run("review", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, sku := oneReviewInventory(t, e, "cancel-review", "10.000", "8.000")
		review, _ := e.a.getInventoryReviewView(context.Background(), inv.ID)
		line := reviewBySKU(t, review)[sku]
		if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, sku, "9.000", line.Version, e.userID); err != nil {
			t.Fatal(err)
		}
		out, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Revisão cancelada com histórico preservado.")
		if err != nil {
			t.Fatal(err)
		}
		if !out.ReferenceRevealed || out.Lines[0].RecountQuantity == nil || *out.Lines[0].RecountQuantity != "9.000" {
			t.Fatalf("review history lost: %+v", out)
		}
	})
	t.Run("invalid reason", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		_, inv, _ := setupTotalCounting(t, e, 2, "10.000")
		for _, reason := range []string{"", "   ", "abc"} {
			if _, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, normalizeAuditText(reason)); !errors.Is(err, errInventoryReasonInvalid) {
				t.Fatalf("reason=%q error=%v", reason, err)
			}
		}
	})
}

func TestInventoryTerminalImmutability(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, sku := oneReviewInventory(t, e, "immutable-completed", "10.000", "10.000")
		review, _ := e.a.getInventoryReviewView(context.Background(), inv.ID)
		version := reviewBySKU(t, review)[sku].Version
		if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, ""); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.a.saveInventoryCount(context.Background(), inv.ID, sku, "9.000", version, e.userID); !errors.Is(err, errInventoryReadOnly) {
			t.Fatalf("count after completed=%v", err)
		}
		if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, sku, "9.000", version, e.userID); !errors.Is(err, errInventoryNotInReview) {
			t.Fatalf("recount after completed=%v", err)
		}
		if _, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Tentativa de trocar terminalidade."); !errors.Is(err, errInventoryCannotCancel) {
			t.Fatalf("cancel after completed=%v", err)
		}
		other := secondInventoryUser(t, e, "completed")
		if _, err := e.a.completeInventory(context.Background(), inv.ID, other, ""); !errors.Is(err, errInventoryTerminal) {
			t.Fatalf("different completion after completed=%v", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		_, inv, skus := setupTotalCounting(t, e, 2, "10.000")
		view, _ := e.a.getInventoryCountingView(context.Background(), inv.ID)
		version := reviewVersionForSKU(view, skus[0])
		if _, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Inventário encerrado antes da contagem."); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.a.saveInventoryCount(context.Background(), inv.ID, skus[0], "10.000", version, e.userID); !errors.Is(err, errInventoryReadOnly) {
			t.Fatalf("count after cancel=%v", err)
		}
		if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, skus[0], "10.000", version, e.userID); !errors.Is(err, errInventoryNotInReview) {
			t.Fatalf("recount after cancel=%v", err)
		}
		if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, ""); !errors.Is(err, errInventoryCannotComplete) {
			t.Fatalf("complete after cancel=%v", err)
		}
		if _, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Outro motivo incompatível."); !errors.Is(err, errInventoryTerminal) {
			t.Fatalf("second different cancel=%v", err)
		}
	})
}

func reviewVersionForSKU(view inventoryCountingView, sku int64) int64 {
	for _, line := range view.Lines {
		if line.SKUID == sku {
			return line.Version
		}
	}
	return -1
}

func TestInventoryTerminalPayloadRejectsSpoofedAuditFields(t *testing.T) {
	cases := []struct {
		dst  any
		body string
	}{
		{&inventoryCompleteRequest{}, `{"completion_note":"ok","completed_by_user_id":99}`},
		{&inventoryCompleteRequest{}, `{"completion_note":"ok","completed_at":"2026-09-30T10:00:00Z"}`},
		{&inventoryCompleteRequest{}, `{"completion_note":"ok","status":"COMPLETED"}`},
		{&inventoryCancelRequest{}, `{"cancellation_reason":"motivo válido","cancelled_by_user_id":99}`},
		{&inventoryCancelRequest{}, `{"cancellation_reason":"motivo válido","cancelled_at":"2026-09-30T10:00:00Z"}`},
		{&inventoryCancelRequest{}, `{"cancellation_reason":"motivo válido","reference_quantity":"10"}`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		rec := httptest.NewRecorder()
		if decodeInventoryJSON(rec, req, tc.dst, false) {
			t.Fatalf("spoof accepted: %s", tc.body)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", tc.body, rec.Code)
		}
	}
}

func TestBlindEarlyCancellationDoesNotRevealReferenceHTTP(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, _ := setupBlindCounting(t, e, 2, "987.321")
	token, csrf := inventorySession(t, e.db, e.userID)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	cancelRec := httptest.NewRecorder()
	mux.ServeHTTP(cancelRec, inventoryRequest(http.MethodPost,
		fmt.Sprintf("/api/inventory-validations/%d/cancel", inv.ID),
		`{"cancellation_reason":"Cancelamento operacional antes da revisão."}`, token, csrf))
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelRec.Code, cancelRec.Body.String())
	}

	targets := []string{
		fmt.Sprintf("/api/inventory-validations/%d", inv.ID),
		fmt.Sprintf("/api/inventory-validations/%d/counting", inv.ID),
		fmt.Sprintf("/api/inventory-validations/%d/review", inv.ID),
		fmt.Sprintf("/api/inventory-validations/%d/history", inv.ID),
	}
	bodies := []string{cancelRec.Body.String()}
	for _, target := range targets {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, inventoryRequest(http.MethodGet, target, "", token, ""))
		bodies = append(bodies, rec.Body.String())
	}
	for _, body := range bodies {
		for _, forbidden := range []string{"987.321", "system_quantity_snapshot", "reference_quantity", "difference", "MATCH", "DIVERGENT", "effective_quantity"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("early BLIND cancellation leaked %q: %s", forbidden, body)
			}
		}
	}

	history, err := e.a.getInventoryHistory(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if history.ReferenceRevealed {
		t.Fatal("early BLIND cancellation must keep reference hidden")
	}
	for _, line := range history.Lines {
		if line.ReferenceQuantity != nil || line.Difference != nil || line.State != nil {
			t.Fatalf("hidden history exposed derived reference: %+v", line)
		}
	}
}

func TestInventoryTerminalConcurrency(t *testing.T) {
	t.Run("complete vs complete", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, _ := oneReviewInventory(t, e, "race-complete", "10.000", "10.000")
		userB := secondInventoryUser(t, e, "race-complete")
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		errs := make([]error, 2)
		go func(){defer wg.Done();<-start;_,errs[0]=e.a.completeInventory(context.Background(),inv.ID,e.userID,"")}()
		go func(){defer wg.Done();<-start;_,errs[1]=e.a.completeInventory(context.Background(),inv.ID,userB,"")}()
		close(start);wg.Wait()
		success:=0
		for _,err:=range errs { if err==nil {success++} }
		if success!=1 { t.Fatalf("complete race errors=%v success=%d",errs,success) }
		h,err:=e.a.getInventoryHistory(context.Background(),inv.ID);if err!=nil{t.Fatal(err)}
		if h.Status!="COMPLETED" || h.CompletedBy=="" || h.CompletedAt==nil {t.Fatalf("history=%+v",h)}
	})

	t.Run("complete vs cancel", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, _ := oneReviewInventory(t, e, "race-terminal", "10.000", "10.000")
		userB := secondInventoryUser(t, e, "race-terminal")
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		errs := make([]error,2)
		go func(){defer wg.Done();<-start;_,errs[0]=e.a.completeInventory(context.Background(),inv.ID,e.userID,"")}()
		go func(){defer wg.Done();<-start;_,errs[1]=e.a.cancelInventory(context.Background(),inv.ID,userB,"Cancelamento concorrente controlado.")}()
		close(start);wg.Wait()
		success:=0
		for _,err:=range errs {if err==nil{success++}}
		if success!=1 {t.Fatalf("terminal race errors=%v success=%d",errs,success)}
		h,err:=e.a.getInventoryHistory(context.Background(),inv.ID);if err!=nil{t.Fatal(err)}
		if h.Status!="COMPLETED" && h.Status!="CANCELLED" {t.Fatalf("status=%s",h.Status)}
	})

	t.Run("recount vs complete", func(t *testing.T) {
		e := newInventoryTestEnv(t)
		inv, sku := oneReviewInventory(t, e, "race-recount", "10.000", "8.000")
		review,_:=e.a.getInventoryReviewView(context.Background(),inv.ID)
		line:=reviewBySKU(t,review)[sku]
		start:=make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var recountErr,completeErr error
		go func(){defer wg.Done();<-start;_,recountErr=e.a.saveInventoryRecount(context.Background(),inv.ID,sku,"10.000",line.Version,e.userID)}()
		go func(){defer wg.Done();<-start;_,completeErr=e.a.completeInventory(context.Background(),inv.ID,e.userID,"")}()
		close(start);wg.Wait()

		var recountQty string
		if err:=e.db.QueryRow(`SELECT quantity::text FROM inventory_validation_recounts WHERE validation_id=$1 AND sku_id=$2`,inv.ID,sku).Scan(&recountQty);err!=nil{
			t.Fatalf("recount was lost: recountErr=%v completeErr=%v db=%v",recountErr,completeErr,err)
		}
		if recountQty!="10.000"{t.Fatalf("recount=%s",recountQty)}
		var status string
		if err:=e.db.QueryRow(`SELECT status FROM inventory_validations WHERE id=$1`,inv.ID).Scan(&status);err!=nil{t.Fatal(err)}
		if status=="REVIEW" {
			if !errors.Is(completeErr,errInventoryCannotComplete) {t.Fatalf("review race completeErr=%v recountErr=%v",completeErr,recountErr)}
			if _,err:=e.a.completeInventory(context.Background(),inv.ID,e.userID,"");err!=nil{t.Fatalf("completion after recount: %v",err)}
		} else if status!="COMPLETED" {
			t.Fatalf("invalid terminal race status=%s",status)
		}
	})
}

func TestInventoryTerminalHTTPACL(t *testing.T) {
	e := newInventoryTestEnv(t)
	inv, _ := oneReviewInventory(t, e, "acl", "10.000", "10.000")
	var profileID,userID int64
	if err:=e.db.QueryRow(`SELECT id FROM profiles WHERE name='Estoquista'`).Scan(&profileID);err!=nil{t.Fatal(err)}
	if _,err:=e.db.Exec(`
INSERT INTO profile_permissions (profile_id,permission_id)
SELECT $1,id FROM permissions
WHERE resource='inventory_validation' AND action='read'
ON CONFLICT DO NOTHING`,profileID);err!=nil{t.Fatal(err)}
	if err:=e.db.QueryRow(`
INSERT INTO users (login,password_hash,profile_id,active,must_change_password)
VALUES ($1,'test',$2,true,false) RETURNING id`,"terminal-readonly-"+t.Name(),profileID).Scan(&userID);err!=nil{t.Fatal(err)}
	token,csrf:=inventorySession(t,e.db,userID)
	mux:=http.NewServeMux();e.a.registerInventoryHandlers(mux)

	completeRec:=httptest.NewRecorder()
	mux.ServeHTTP(completeRec,inventoryRequest(http.MethodPost,fmt.Sprintf("/api/inventory-validations/%d/complete",inv.ID),`{"completion_note":""}`,token,csrf))
	if completeRec.Code!=http.StatusForbidden{t.Fatalf("complete status=%d body=%s",completeRec.Code,completeRec.Body.String())}

	cancelRec:=httptest.NewRecorder()
	mux.ServeHTTP(cancelRec,inventoryRequest(http.MethodPost,fmt.Sprintf("/api/inventory-validations/%d/cancel",inv.ID),`{"cancellation_reason":"Motivo autorizado apenas para escrita."}`,token,csrf))
	if cancelRec.Code!=http.StatusForbidden{t.Fatalf("cancel status=%d body=%s",cancelRec.Code,cancelRec.Body.String())}
}

func TestInventoryTerminalAntiMovement(t *testing.T) {
	e:=newInventoryTestEnv(t)
	ctx:=context.Background()

	wh1:=e.warehouse(t,"anti-match",true);sku1:=e.sku(t,"anti-match-sku",true,true);e.movementDecimal(t,sku1,wh1,"IN","10.000")
	matchInv,err:=e.a.createTotalInventory(ctx,e.userID,wh1);if err!=nil{t.Fatal(err)}

	wh2:=e.warehouse(t,"anti-div",true);sku2:=e.sku(t,"anti-div-sku",true,true);e.movementDecimal(t,sku2,wh2,"IN","10.000")
	divInv,err:=e.a.createTotalInventory(ctx,e.userID,wh2);if err!=nil{t.Fatal(err)}

	wh3:=e.warehouse(t,"anti-open",true);sku3:=e.sku(t,"anti-open-sku",true,true);e.movementDecimal(t,sku3,wh3,"IN","10.000")
	openInv,err:=e.a.createTotalInventory(ctx,e.userID,wh3);if err!=nil{t.Fatal(err)}

	wh4:=e.warehouse(t,"anti-counting",true);a:=e.sku(t,"anti-counting-a",true,true);b:=e.sku(t,"anti-counting-b",true,true);e.movementDecimal(t,a,wh4,"IN","10.000");e.movementDecimal(t,b,wh4,"IN","10.000")
	countingInv,err:=e.a.createTotalInventory(ctx,e.userID,wh4);if err!=nil{t.Fatal(err)}

	wh5:=e.warehouse(t,"anti-review",true);sku5:=e.sku(t,"anti-review-sku",true,true);e.movementDecimal(t,sku5,wh5,"IN","10.000")
	reviewInv,err:=e.a.createTotalInventory(ctx,e.userID,wh5);if err!=nil{t.Fatal(err)}

	var movementsBefore,linesBefore int64
	var stockBefore string
	if err:=e.db.QueryRow(`SELECT count(*) FROM movements`).Scan(&movementsBefore);err!=nil{t.Fatal(err)}
	if err:=e.db.QueryRow(`SELECT count(*) FROM movement_lines`).Scan(&linesBefore);err!=nil{t.Fatal(err)}
	if err:=e.db.QueryRow(`SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text FROM movement_lines`).Scan(&stockBefore);err!=nil{t.Fatal(err)}

	countInventoryToReview(t,e,matchInv,map[int64]string{sku1:"10.000"})
	if _,err:=e.a.completeInventory(ctx,matchInv.ID,e.userID,"");err!=nil{t.Fatal(err)}

	countInventoryToReview(t,e,divInv,map[int64]string{sku2:"8.000"})
	r2,_:=e.a.getInventoryReviewView(ctx,divInv.ID);l2:=reviewBySKU(t,r2)[sku2]
	if _,err:=e.a.saveInventoryRecount(ctx,divInv.ID,sku2,"9.000",l2.Version,e.userID);err!=nil{t.Fatal(err)}
	if _,err:=e.a.completeInventory(ctx,divInv.ID,e.userID,"Divergência confirmada e preservada.");err!=nil{t.Fatal(err)}

	if _,err:=e.a.cancelInventory(ctx,openInv.ID,e.userID,"Cancelamento no estado aberto.");err!=nil{t.Fatal(err)}

	cv,_:=e.a.getInventoryCountingView(ctx,countingInv.ID)
	if _,_,err:=e.a.saveInventoryCount(ctx,countingInv.ID,cv.Lines[0].SKUID,"10.000",cv.Lines[0].Version,e.userID);err!=nil{t.Fatal(err)}
	if _,err:=e.a.cancelInventory(ctx,countingInv.ID,e.userID,"Cancelamento durante contagem parcial.");err!=nil{t.Fatal(err)}

	countInventoryToReview(t,e,reviewInv,map[int64]string{sku5:"10.000"})
	if _,err:=e.a.cancelInventory(ctx,reviewInv.ID,e.userID,"Cancelamento durante revisão formal.");err!=nil{t.Fatal(err)}

	for _,id:=range []int64{matchInv.ID,divInv.ID,openInv.ID,countingInv.ID,reviewInv.ID}{
		if _,err:=e.a.getInventoryHistory(ctx,id);err!=nil{t.Fatal(err)}
	}

	var movementsAfter,linesAfter int64
	var stockAfter string
	_ = e.db.QueryRow(`SELECT count(*) FROM movements`).Scan(&movementsAfter)
	_ = e.db.QueryRow(`SELECT count(*) FROM movement_lines`).Scan(&linesAfter)
	_ = e.db.QueryRow(`SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text FROM movement_lines`).Scan(&stockAfter)
	if movementsBefore!=movementsAfter||linesBefore!=linesAfter||stockBefore!=stockAfter{
		t.Fatalf("terminal lifecycle changed stock: movements %d->%d lines %d->%d stock %s->%s",movementsBefore,movementsAfter,linesBefore,linesAfter,stockBefore,stockAfter)
	}
}

func TestInventoryTerminalMigration018CleanAndIncremental(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL")==""{t.Skip("TEST_DATABASE_URL is required")}
	ctx:=context.Background();migrationsDir:=filepath.Join("..","..","migrations");db:=openTestDatabase(t)
	baseDir:=t.TempDir()
	for i:=1;i<=17;i++{
		matches,err:=filepath.Glob(filepath.Join(migrationsDir,fmt.Sprintf("%03d_*.sql",i)))
		if err!=nil||len(matches)!=1{t.Fatalf("migration %03d matches=%v err=%v",i,matches,err)}
		body,err:=os.ReadFile(matches[0]);if err!=nil{t.Fatal(err)}
		if err:=os.WriteFile(filepath.Join(baseDir,filepath.Base(matches[0])),body,0o600);err!=nil{t.Fatal(err)}
	}
	if err:=runMigrations(ctx,db,baseDir);err!=nil{t.Fatalf("001-017: %v",err)}

	var movementSchemaBefore string
	if err:=db.QueryRowContext(ctx,`
SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name='movement_lines'`).Scan(&movementSchemaBefore);err!=nil{t.Fatal(err)}

	mig:=filepath.Join(migrationsDir,"018_inventory_terminal_audit.sql");body,err:=os.ReadFile(mig);if err!=nil{t.Fatal(err)}
	if strings.Contains(strings.ToLower(string(body)),"movement_lines"){t.Fatal("migration 018 must not touch movement_lines")}
	inc:=t.TempDir();if err:=os.WriteFile(filepath.Join(inc,filepath.Base(mig)),body,0o600);err!=nil{t.Fatal(err)}
	if err:=runMigrations(ctx,db,inc);err!=nil{t.Fatalf("incremental 018: %v",err)}

	var movementSchemaAfter string
	if err:=db.QueryRowContext(ctx,`
SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name='movement_lines'`).Scan(&movementSchemaAfter);err!=nil{t.Fatal(err)}
	if movementSchemaBefore!=movementSchemaAfter{t.Fatal("movement_lines changed by 018")}

	for _,col:=range []string{"completed_by_user_id","completion_note","cancelled_by_user_id","cancellation_reason"}{
		var exists bool
		if err:=db.QueryRowContext(ctx,`
SELECT EXISTS(
 SELECT 1 FROM information_schema.columns
 WHERE table_schema=current_schema() AND table_name='inventory_validations' AND column_name=$1
)`,col).Scan(&exists);err!=nil{t.Fatal(err)}
		if !exists{t.Fatalf("missing column %s",col)}
	}

	cleanDB:=openTestDatabase(t)
	if err:=runMigrations(ctx,cleanDB,migrationsDir);err!=nil{t.Fatalf("clean 001-018: %v",err)}
}

func TestInventoryTerminalIdempotentRetryDoesNotMaskDifferentIntent(t *testing.T) {
	e:=newInventoryTestEnv(t)
	inv,_:=oneReviewInventory(t,e,"idempotent","10.000","10.000")
	first,err:=e.a.completeInventory(context.Background(),inv.ID,e.userID,"")
	if err!=nil{t.Fatal(err)}
	retry,err:=e.a.completeInventory(context.Background(),inv.ID,e.userID,"")
	if err!=nil{t.Fatalf("identical retry: %v",err)}
	if first.CompletedAt==nil||retry.CompletedAt==nil||!first.CompletedAt.Equal(*retry.CompletedAt){t.Fatalf("retry changed completed_at: first=%v retry=%v",first.CompletedAt,retry.CompletedAt)}
	if _,err:=e.a.cancelInventory(context.Background(),inv.ID,e.userID,"Intenção terminal diferente.");!errors.Is(err,errInventoryCannotCancel){t.Fatalf("different terminal intent=%v",err)}
}

func TestInventoryHistoryJSONEarlyBlindOmitsReferenceKeys(t *testing.T) {
	e:=newInventoryTestEnv(t)
	_,inv,_:=setupBlindCounting(t,e,2,"987.321")
	out,err:=e.a.cancelInventory(context.Background(),inv.ID,e.userID,"Cancelamento cego com histórico preservado.")
	if err!=nil{t.Fatal(err)}
	body,err:=json.Marshal(out);if err!=nil{t.Fatal(err)}
	s:=string(body)
	for _,forbidden:=range []string{"987.321","reference_quantity","effective_quantity","difference","state"}{
		if strings.Contains(s,forbidden){t.Fatalf("marshaled history leaked %q: %s",forbidden,s)}
	}
}
