package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func g7perm(t *testing.T, e inventoryTestEnv, resource, action string) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow("SELECT id FROM permissions WHERE resource=$1 AND action=$2", resource, action).Scan(&id); err != nil { t.Fatal(err) }
	return id
}
func g7profile(t *testing.T, e inventoryTestEnv, name string, perms ...int64) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow("INSERT INTO profiles (name) VALUES ($1) RETURNING id", t.Name()+"-"+name).Scan(&id); err != nil { t.Fatal(err) }
	for _, p := range perms {
		if _, err := e.db.Exec("INSERT INTO profile_permissions (profile_id,permission_id) VALUES ($1,$2)", id, p); err != nil { t.Fatal(err) }
	}
	return id
}
func g7user(t *testing.T, e inventoryTestEnv, profileID int64, label string) int64 {
	t.Helper()
	var id int64
	if err := e.db.QueryRow("INSERT INTO users (login,password_hash,profile_id,active,must_change_password) VALUES ($1,'test',$2,true,false) RETURNING id", "g7-"+label+"-"+t.Name(), profileID).Scan(&id); err != nil { t.Fatal(err) }
	return id
}
func g7serve(a *app, method, target, body, token, csrf string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	a.registerUIHandlers(mux)
	a.registerAdminHandlers(mux)
	a.registerInventoryHandlers(mux)
	req := inventoryRequest(method, target, body, token, csrf)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestInventoryGate07ACLNavigationAndAdminPermissions(t *testing.T) {
	e := newInventoryTestEnv(t)
	stockRead := g7perm(t,e,"stock","read")
	invRead := g7perm(t,e,"inventory_validation","read")
	invWrite := g7perm(t,e,"inventory_validation","write")

	noProfile := g7profile(t,e,"none",stockRead)
	noUser := g7user(t,e,noProfile,"none")
	noToken,_ := inventorySession(t,e.db,noUser)
	noStock := g7serve(e.a,http.MethodGet,"/stock","",noToken,"")
	if noStock.Code != http.StatusOK { t.Fatalf("no inventory stock=%d",noStock.Code) }
	if strings.Contains(noStock.Body.String(),"Validação de Estoque") { t.Fatal("navigation leaked inventory module to no-read user") }
	noPage := g7serve(e.a,http.MethodGet,"/inventory","",noToken,"")
	if noPage.Code != http.StatusSeeOther || noPage.Header().Get("Location")!="/no-access" { t.Fatalf("no-read page=%d loc=%s",noPage.Code,noPage.Header().Get("Location")) }
	if rr:=g7serve(e.a,http.MethodGet,"/api/inventory-validations","",noToken,""); rr.Code!=http.StatusForbidden { t.Fatalf("no-read list=%d",rr.Code) }

	readProfile := g7profile(t,e,"read",stockRead,invRead)
	readUser := g7user(t,e,readProfile,"read")
	readToken,readCSRF := inventorySession(t,e.db,readUser)
	readStock := g7serve(e.a,http.MethodGet,"/stock","",readToken,"")
	if readStock.Code!=http.StatusOK || !strings.Contains(readStock.Body.String(),"Validação de Estoque") { t.Fatalf("read navigation=%d body=%s",readStock.Code,readStock.Body.String()) }
	readPage := g7serve(e.a,http.MethodGet,"/inventory","",readToken,"")
	if readPage.Code!=http.StatusOK { t.Fatalf("read page=%d body=%s",readPage.Code,readPage.Body.String()) }
	if strings.Contains(readPage.Body.String(),"inventoryCreateTotal") || strings.Contains(readPage.Body.String(),"inventoryCreateBlind") { t.Fatal("read-only page exposes creation controls") }
	if rr:=g7serve(e.a,http.MethodGet,"/api/inventory-validations","",readToken,""); rr.Code!=http.StatusOK { t.Fatalf("read list=%d body=%s",rr.Code,rr.Body.String()) }
	if rr:=g7serve(e.a,http.MethodGet,"/api/inventory-validations/warehouses","",readToken,""); rr.Code!=http.StatusOK { t.Fatalf("read warehouses=%d",rr.Code) }
	for _,tc:=range []struct{m,u,b string}{
		{http.MethodPost,"/api/inventory-validations/total","{\"warehouse_id\":1}"},
		{http.MethodPost,"/api/inventory-validations/blind","{}"},
		{http.MethodPut,"/api/inventory-validations/1/lines/1/count","{\"physical_quantity\":\"1\",\"expected_version\":0}"},
		{http.MethodPut,"/api/inventory-validations/1/lines/1/recount","{\"physical_quantity\":\"1\",\"expected_version\":0}"},
		{http.MethodPost,"/api/inventory-validations/1/complete","{\"completion_note\":\"\"}"},
		{http.MethodPost,"/api/inventory-validations/1/cancel","{\"cancellation_reason\":\"Motivo válido\"}"},
	} {
		rr:=g7serve(e.a,tc.m,tc.u,tc.b,readToken,readCSRF)
		if rr.Code!=http.StatusForbidden { t.Fatalf("read-only %s %s=%d body=%s",tc.m,tc.u,rr.Code,rr.Body.String()) }
	}

	rwProfile := g7profile(t,e,"rw",stockRead)
	dirToken,dirCSRF := inventorySession(t,e.db,e.userID)
	adminMux:=http.NewServeMux(); e.a.registerAdminHandlers(adminMux)
	adminReq:=inventoryRequest(http.MethodPut,fmt.Sprintf("/api/admin/profiles/%d/permissions",rwProfile),fmt.Sprintf("{\"permission_ids\":[%d,%d,%d]}",stockRead,invRead,invWrite),dirToken,dirCSRF)
	adminRec:=httptest.NewRecorder(); adminMux.ServeHTTP(adminRec,adminReq)
	if adminRec.Code!=http.StatusOK { t.Fatalf("existing admin permission update=%d body=%s",adminRec.Code,adminRec.Body.String()) }
	rwUser:=g7user(t,e,rwProfile,"rw")
	rwToken,rwCSRF:=inventorySession(t,e.db,rwUser)
	rwPage:=g7serve(e.a,http.MethodGet,"/inventory","",rwToken,"")
	if rwPage.Code!=http.StatusOK || !strings.Contains(rwPage.Body.String(),"inventoryCreateTotal") || !strings.Contains(rwPage.Body.String(),"inventoryCreateBlind") { t.Fatalf("rw page=%d body=%s",rwPage.Code,rwPage.Body.String()) }

	wh:=e.warehouse(t,"acl-total",true); sku:=e.sku(t,"acl-total-sku",true,true); e.movementDecimal(t,sku,wh,"IN","5.000")
	total:=g7serve(e.a,http.MethodPost,"/api/inventory-validations/total",fmt.Sprintf("{\"warehouse_id\":%d}",wh),rwToken,rwCSRF)
	if total.Code!=http.StatusCreated { t.Fatalf("rw create total=%d body=%s",total.Code,total.Body.String()) }

	blindWH:=e.warehouse(t,"acl-blind",true)
	blindA:=e.sku(t,"acl-blind-a",true,true)
	blindB:=e.sku(t,"acl-blind-b",true,true)
	e.movementDecimal(t,blindA,blindWH,"IN","4.000")
	e.movementDecimal(t,blindB,blindWH,"IN","4.000")
	blind:=g7serve(e.a,http.MethodPost,"/api/inventory-validations/blind","{}",rwToken,rwCSRF)
	if blind.Code!=http.StatusCreated { t.Fatalf("rw create blind=%d body=%s",blind.Code,blind.Body.String()) }
}

func TestInventoryGate07ListBlindSecrecyAndFilters(t *testing.T) {
	e:=newInventoryTestEnv(t); ctx:=context.Background()
	wh:=e.warehouse(t,"list",true); a:=e.sku(t,"list-a",true,true); b:=e.sku(t,"list-b",true,true)
	e.movementDecimal(t,a,wh,"IN","987.321"); e.movementDecimal(t,b,wh,"IN","987.321")
	blind,err:=e.a.createBlindInventory(ctx,e.userID); if err!=nil{t.Fatal(err)}
	token,_:=inventorySession(t,e.db,e.userID)
	rec:=g7serve(e.a,http.MethodGet,"/api/inventory-validations?status=OPEN&type=BLIND&warehouse_id="+strconv.FormatInt(blind.WarehouseID,10)+"&limit=30&offset=0","",token,"")
	if rec.Code!=http.StatusOK {t.Fatalf("list=%d body=%s",rec.Code,rec.Body.String())}
	body:=rec.Body.String()
	if !strings.Contains(body,fmt.Sprintf("\"id\":%d",blind.ID)){t.Fatalf("blind missing: %s",body)}
	for _,bad:=range []string{"987.321","system_quantity_snapshot","reference_quantity","difference","MATCH","DIVERGENT","effective_quantity"}{if strings.Contains(body,bad){t.Fatalf("list leaked %q: %s",bad,body)}}
	if _,err:=e.a.cancelInventory(ctx,blind.ID,e.userID,"Cancelamento precoce para regressão BLIND.");err!=nil{t.Fatal(err)}
	rec=g7serve(e.a,http.MethodGet,"/api/inventory-validations?status=CANCELLED&type=BLIND&limit=30&offset=0","",token,"")
	for _,bad:=range []string{"987.321","reference_quantity","difference","MATCH","DIVERGENT"}{if strings.Contains(rec.Body.String(),bad){t.Fatalf("cancelled list leaked %q",bad)}}
	for _,url:=range []string{"/api/inventory-validations?status=BAD","/api/inventory-validations?type=BAD","/api/inventory-validations?warehouse_id=-1","/api/inventory-validations?from=30-09-2026","/api/inventory-validations?limit=1000","/api/inventory-validations?offset=-1"}{
		rr:=g7serve(e.a,http.MethodGet,url,"",token,""); if rr.Code!=http.StatusBadRequest{t.Fatalf("bad query %s=%d",url,rr.Code)}
	}
}

func TestInventoryGate07ResumeReviewHistory(t *testing.T){
	e:=newInventoryTestEnv(t); ctx:=context.Background(); _,inv,_:=setupTotalCounting(t,e,2,"10.000")
	view,_:=e.a.getInventoryCountingView(ctx,inv.ID); first:=view.Lines[0]
	if _,_,err:=e.a.saveInventoryCount(ctx,inv.ID,first.SKUID,"7.125",first.Version,e.userID);err!=nil{t.Fatal(err)}
	resumed,_:=e.a.getInventoryCountingView(ctx,inv.ID)
	var found bool
	for _,line:=range resumed.Lines{if line.SKUID==first.SKUID{found=line.PhysicalQuantity!=nil&&*line.PhysicalQuantity=="7.125"&&line.Version==first.Version+1}}
	if !found||resumed.Counted!=1{t.Fatalf("resume lost state: %+v",resumed)}
	for _,line:=range resumed.Lines{if line.SKUID!=first.SKUID{if _,_,err:=e.a.saveInventoryCount(ctx,inv.ID,line.SKUID,"10.000",line.Version,e.userID);err!=nil{t.Fatal(err)}}}
	review,err:=e.a.getInventoryReviewView(ctx,inv.ID);if err!=nil{t.Fatal(err)}
	var divergent inventoryReviewLine
	for _,line:=range review.Lines{if line.State=="DIVERGENT"{divergent=line}}
	if divergent.SKUID==0||!divergent.RecountAvailable{t.Fatalf("review missing divergence: %+v",review)}
	if _,err:=e.a.saveInventoryRecount(ctx,inv.ID,divergent.SKUID,"10.000",divergent.Version,e.userID);err!=nil{t.Fatal(err)}
	if _,err:=e.a.completeInventory(ctx,inv.ID,e.userID,"");err!=nil{t.Fatal(err)}
	h,err:=e.a.getInventoryHistory(ctx,inv.ID);if err!=nil{t.Fatal(err)}
	if h.Status!="COMPLETED"{t.Fatalf("history status=%s",h.Status)}
	token,_:=inventorySession(t,e.db,e.userID)
	historyHTTP:=g7serve(e.a,http.MethodGet,fmt.Sprintf("/api/inventory-validations/%d/history",inv.ID),"",token,"")
	if historyHTTP.Code!=http.StatusOK || !strings.Contains(historyHTTP.Body.String(),"\"status\":\"COMPLETED\""){t.Fatalf("history HTTP=%d body=%s",historyHTTP.Code,historyHTTP.Body.String())}
}

func TestInventoryGate07AntiMovement(t *testing.T){
	e:=newInventoryTestEnv(t);ctx:=context.Background()
	wh:=e.warehouse(t,"anti",true);a:=e.sku(t,"anti-a",true,true);b:=e.sku(t,"anti-b",true,true)
	e.movementDecimal(t,a,wh,"IN","10.000");e.movementDecimal(t,b,wh,"IN","10.000")
	var mb,lb int64;var sb string
	e.db.QueryRow("SELECT count(*) FROM movements").Scan(&mb);e.db.QueryRow("SELECT count(*) FROM movement_lines").Scan(&lb);e.db.QueryRow("SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text FROM movement_lines").Scan(&sb)
	token,csrf:=inventorySession(t,e.db,e.userID);mux:=http.NewServeMux();e.a.registerInventoryHandlers(mux)
	list:=httptest.NewRecorder();mux.ServeHTTP(list,inventoryRequest(http.MethodGet,"/api/inventory-validations","",token,""));if list.Code!=http.StatusOK{t.Fatalf("list=%d",list.Code)}
	create:=httptest.NewRecorder();mux.ServeHTTP(create,inventoryRequest(http.MethodPost,"/api/inventory-validations/total",fmt.Sprintf("{\"warehouse_id\":%d}",wh),token,csrf));if create.Code!=http.StatusCreated{t.Fatalf("create=%d %s",create.Code,create.Body.String())}
	var inv inventorySelection;if err:=json.Unmarshal(create.Body.Bytes(),&inv);err!=nil{t.Fatal(err)}
	view,_:=e.a.getInventoryCountingView(ctx,inv.ID)
	for _,line:=range view.Lines{q:="10.000";if line.SKUID==a{q="8.000"};if _,_,err:=e.a.saveInventoryCount(ctx,inv.ID,line.SKUID,q,line.Version,e.userID);err!=nil{t.Fatal(err)}}
	review,_:=e.a.getInventoryReviewView(ctx,inv.ID);line:=reviewBySKU(t,review)[a]
	if _,err:=e.a.saveInventoryRecount(ctx,inv.ID,a,"9.000",line.Version,e.userID);err!=nil{t.Fatal(err)}
	if _,err:=e.a.completeInventory(ctx,inv.ID,e.userID,"Divergência confirmada sem ajuste.");err!=nil{t.Fatal(err)}
	if _,err:=e.a.getInventoryHistory(ctx,inv.ID);err!=nil{t.Fatal(err)}
	cancelInv,err:=e.a.createTotalInventory(ctx,e.userID,wh);if err!=nil{t.Fatal(err)}
	cancel:=httptest.NewRecorder();mux.ServeHTTP(cancel,inventoryRequest(http.MethodPost,fmt.Sprintf("/api/inventory-validations/%d/cancel",cancelInv.ID),"{\"cancellation_reason\":\"Cancelamento administrativo controlado.\"}",token,csrf));if cancel.Code!=http.StatusOK{t.Fatalf("cancel=%d %s",cancel.Code,cancel.Body.String())}
	var ma,la int64;var sa string
	e.db.QueryRow("SELECT count(*) FROM movements").Scan(&ma);e.db.QueryRow("SELECT count(*) FROM movement_lines").Scan(&la);e.db.QueryRow("SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text FROM movement_lines").Scan(&sa)
	if mb!=ma||lb!=la||sb!=sa{t.Fatalf("stock changed: movements %d->%d lines %d->%d stock %s->%s",mb,ma,lb,la,sb,sa)}
}

func TestInventoryGate07UIRoutesAndNoDuplication(t *testing.T){
	jsb,err:=os.ReadFile(filepath.Join("static","app.js"));if err!=nil{t.Fatal(err)}
	hb,err:=os.ReadFile(filepath.Join("templates","inventory.html"));if err!=nil{t.Fatal(err)}
	cb,err:=os.ReadFile(filepath.Join("static","app.css"));if err!=nil{t.Fatal(err)}
	js,page,css:=string(jsb),string(hb),string(cb)
	for _,want:=range []string{"Validação de Estoque","inventoryCreateTotal","inventoryCreateBlind","inventoryListFilters","inventoryList","Warehouse e itens serão escolhidos automaticamente","quantidades esperadas permanecerão ocultas","Continuar contagem","Revisar inventário","Ver histórico","CanInventoryWrite"}{if !strings.Contains(page+js,want){t.Fatalf("UI missing %q",want)}}
	if strings.Count(js,"initScanner('scanInventory'")!=1{t.Fatal("inventory must reuse one scanner callback")}
	start:=strings.Index(js,"function initInventory(){");end:=strings.Index(js[start:],"function initForcedPassword()");if start<0||end<0{t.Fatal("inventory JS boundary missing")};invJS:=js[start:start+end]
	for _,bad:=range []string{"system_quantity_snapshot","createEntry","createExit","createTransfer","stock_locks","Ajustar estoque","/api/movements"}{if strings.Contains(invJS,bad){t.Fatalf("inventory JS contains %q",bad)}}
	if !strings.Contains(css,".inventory-create-grid,.inventory-list-filters{grid-template-columns:1fr}"){t.Fatal("mobile administrative layout missing")}
	for _,name:=range []string{"inventory.go","inventory_counting.go","inventory_review.go","inventory_terminal.go","inventory_admin.go"}{
		body,err:=os.ReadFile(name);if err!=nil{t.Fatal(err)}
		for _,line:=range strings.Split(string(body),"\n"){if strings.Contains(line,"HandleFunc(")&&strings.Contains(line,"/api/inventory")&&!strings.Contains(line,"requireAuth("){t.Fatalf("unauth inventory endpoint %s: %s",name,line)}}
	}
	if !strings.Contains(js,"r==='inventory_validation'?'Validação de Estoque':r"){t.Fatal("existing admin does not label inventory permissions")}
}
