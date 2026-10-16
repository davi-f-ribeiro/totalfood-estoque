// Tela de expedição: entregas do dia, rotas, romaneio. A tela só monta pedidos para a
// API; regras (transições, uma rota aberta por entrega, versão) ficam no servidor.
(async()=>{
const root=document.getElementById('dispatchRoot');if(!root)return;
const canWrite=root.dataset.canWrite==='1',$=id=>document.getElementById(id),status=$('dispatchStatus');
const orderLabel={criado:'Nova',programado:'Programada',despachado:'No caminhão',em_rota:'A caminho',chegou:'No local',entregue:'Entregue',falhou:'Não entregue',cancelado:'Cancelada'};
const routeLabel={rascunho:'Rascunho',liberado:'Liberada',em_andamento:'Em andamento',concluido:'Concluída',cancelado:'Cancelada'};
const mealLabel={cafe:'Café',almoco:'Almoço',lanche:'Lanche',jantar:'Jantar'};
const proofLabel={foto:'Foto',assinatura:'Assinatura',codigo:'Código'};
const routable=s=>s==='criado'||s==='programado'||s==='falhou';
const nf=new Intl.NumberFormat('pt-BR',{maximumFractionDigits:1});
let orders=[],routes=[],options=null,route=null,stops=[],dirty=false,warnings=[];
const today=new Date(Date.now()-new Date().getTimezoneOffset()*60000).toISOString().slice(0,10);
$('dispatchDay').value=today;
// Os painéis usam <form> só pelo estilo do diálogo; Enter não envia nada.
document.querySelectorAll('.dispatch-panel').forEach(f=>f.addEventListener('submit',e=>e.preventDefault()));
const day=()=>$('dispatchDay').value||today;

function el(tag,cls,text){const e=document.createElement(tag);if(cls)e.className=cls;if(text!=null)e.textContent=text;return e}
function chip(kind,label){return el('span','dispatch-chip '+kind,label)}
function card(title,chipEl,detail,onOpen){const b=el('button','catalog-parent-card');b.type='button';const head=el('strong',null,title+' ');head.append(chipEl);b.append(head);
 const d=detail.filter(Boolean);if(d.length)b.append(el('small',null,d.join(' · ')));b.onclick=onOpen;return b}
function fillSelect(sel,rows,empty){sel.textContent='';if(empty!=null){const o=el('option',null,empty);o.value='';sel.append(o)}rows.forEach(r=>{const o=el('option',null,r.name);o.value=r.id;sel.append(o)})}
function km(m){return m==null?null:nf.format(m/1000)+' km'}
function minutes(s){if(s==null)return null;const h=Math.floor(s/3600),m=Math.round(s%3600/60);return h?h+' h '+m+' min':m+' min'}

async function loadAll(){
 setFeedback(status,'loading','Carregando a expedição…');
 try{[orders,routes]=await Promise.all([api('/api/delivery-orders?day='+day()),api('/api/routes?day='+day())])}
 catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar a expedição. Atualize a página.'));return}
 setFeedback(status,'','');renderOrders();renderRoutes()}
function renderOrders(){const list=$('orderList');list.textContent='';
 if(!orders.length){list.append(el('p','hint','Nenhuma entrega neste dia.'));return}
 orders.forEach(o=>list.append(card(o.place_name,chip(o.status,orderLabel[o.status]||o.status),
  [o.meal?mealLabel[o.meal]:null,'Combinado '+o.due_at,o.route_id?'Rota #'+o.route_id:(routable(o.status)?'Sem rota':null),o.has_coordinates?null:'Sem coordenada',o.lines+(o.lines===1?' item':' itens')],()=>openOrder(o.id))))}
function renderRoutes(){const list=$('routeList');list.textContent='';
 if(!routes.length){list.append(el('p','hint','Nenhuma rota neste dia.'));return}
 routes.forEach(r=>list.append(card('Rota #'+r.id+' · '+r.vehicle_plate,chip(r.status,routeLabel[r.status]||r.status),
  [r.driver_name,r.vehicle_ownership==='alugada'?'Veículo alugado':null,r.stops+(r.stops===1?' entrega':' entregas'),km(r.total_distance_m),minutes(r.total_duration_s)],()=>openRoute(r.id))))}
$('dispatchDay').addEventListener('change',loadAll);

// --- detalhe da entrega -----------------------------------------------------------
let detail=null;
async function openOrder(id){const body=$('orderDetailBody');body.textContent='';setFeedback($('orderDetailStatus'),'','');
 try{detail=await api('/api/delivery-orders/'+id)}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível abrir a entrega.'));return}
 const o=detail;$('orderDetailTitle').textContent='Entrega #'+o.id+' · '+o.place_name;
 body.append(el('p',null,[orderLabel[o.status],o.meal?mealLabel[o.meal]:null,'Combinado '+o.due_at,'Sai de '+o.origin_warehouse,o.pod_required?'Exige comprovante':null].filter(Boolean).join(' · ')));
 if(o.notes)body.append(el('p','hint',o.notes));
 body.append(el('h3',null,'O que vai'));const ul=el('ul');
 o.lines.forEach(l=>ul.append(el('li',null,l.recipe_id?l.recipe_name+' · '+l.portions+' porções':l.item_name+' · '+displayDecimal(l.quantity)+(l.unit?' '+l.unit:''))));body.append(ul);
 body.append(el('h3',null,'Histórico'));const ev=el('ul');
 if(!o.events.length)ev.append(el('li',null,'Sem movimentação ainda.'));
 o.events.forEach(e=>ev.append(el('li',null,new Date(e.occurred_at).toLocaleTimeString('pt-BR',{hour:'2-digit',minute:'2-digit'})+' · '+(orderLabel[e.status]||e.status)+(e.notes?' — '+e.notes:'')+(e.latitude?' · com localização':''))));body.append(ev);
 if(o.proofs.length){body.append(el('h3',null,'Comprovante'));o.proofs.forEach(p=>{
  body.append(el('p',null,[proofLabel[p.kind],p.receiver_name?'Recebido por '+p.receiver_name:null,p.temperature_c?displayDecimal(p.temperature_c,1)+' °C':null].filter(Boolean).join(' · ')));
  if(p.has_image){const img=el('img','dispatch-proof');img.alt=proofLabel[p.kind]+' da entrega';img.src='/api/delivery-proofs/'+p.id+'/content';body.append(img)}})}
 if($('orderCancel'))$('orderCancel').hidden=!(o.next_statuses||[]).includes('cancelado');
 $('orderDetailDialog').showModal()}
$('orderDetailClose').onclick=()=>$('orderDetailDialog').close();
if($('orderCancel'))$('orderCancel').onclick=()=>runLocked($('orderCancel'),async()=>{
 if(!confirm('Cancelar esta entrega? Ela não poderá ser reaberta.'))return;
 try{await api('/api/delivery-orders/'+detail.id+'/events',{method:'POST',body:JSON.stringify({operation_id:newOperationID(),status:'cancelado'})});$('orderDetailDialog').close();await loadAll();setFeedback(status,'success','Entrega cancelada.')}
 catch(err){setFeedback($('orderDetailStatus'),'error',uiErrorMessage(err,'Não foi possível cancelar a entrega.'))}});

// --- rota ----------------------------------------------------------------------
async function openRoute(id,message){
 try{route=await api('/api/routes/'+id)}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível abrir a rota.'));return}
 stops=route.stops.map(s=>({order_id:s.order_id,place_name:s.place_name,address:s.address,status:s.status,eta:s.eta,window:s.window_start?s.window_start+'–'+s.window_end:null}));dirty=false;
 renderRoute();setFeedback($('routeWarnings'),warnings.length?'error':'',warnings.join(' '));warnings=[];setFeedback($('routeStatus'),message?'success':'',message||'');
 if(!$('routeDialog').open)$('routeDialog').showModal()}
function editable(){return canWrite&&route&&route.status==='rascunho'}
function renderRoute(){const r=route;
 $('routeDialogTitle').textContent='Rota #'+r.id+' · '+r.vehicle_plate+' · '+routeLabel[r.status];
 $('routeSummary').textContent=[r.driver_name,r.vehicle_description,r.vehicle_ownership==='alugada'?'alugado':null,r.vehicle_refrigerated?'refrigerado':null,'até '+displayDecimal(r.vehicle_capacity_kg)+' kg',km(r.total_distance_m),minutes(r.total_duration_s)].filter(Boolean).join(' · ');
 const list=$('routeStops');list.textContent='';
 if(!stops.length)list.append(el('li',null,'Nenhuma entrega nesta rota.'));
 stops.forEach((s,i)=>{const li=el('li');li.append(el('span','seq',String(i+1)));const t=el('div');t.append(el('strong',null,s.place_name),el('small',null,[s.address,s.window?'Recebe '+s.window:null,s.eta&&!dirty?'Previsão '+s.eta:null,orderLabel[s.status]].filter(Boolean).join(' · ')));li.append(t);
  if(editable()){const m=el('div','move');
   const mv=(d)=>{const j=i+d;if(j<0||j>=stops.length)return;[stops[i],stops[j]]=[stops[j],stops[i]];dirty=true;renderRoute()};
   const up=el('button','secondary','↑');up.type='button';up.setAttribute('aria-label','Subir '+s.place_name);up.onclick=()=>mv(-1);up.disabled=i===0;
   const dn=el('button','secondary','↓');dn.type='button';dn.setAttribute('aria-label','Descer '+s.place_name);dn.onclick=()=>mv(1);dn.disabled=i===stops.length-1;
   const rm=el('button','secondary','Tirar');rm.type='button';rm.onclick=()=>{stops.splice(i,1);dirty=true;renderRoute()};
   m.append(up,dn,rm);li.append(m)}
  list.append(li)});
 const ed=editable();
 ['routeAddBox','routeDepartureField','routeSave','routeOptimize','routeKeep'].forEach(id=>{if($(id))$(id).hidden=!ed});
 if($('routeRelease'))$('routeRelease').hidden=!(ed&&stops.length);
 if($('routeCancel'))$('routeCancel').hidden=!(canWrite&&(r.status==='rascunho'||r.status==='liberado'));
 if(ed){const add=$('routeAddList');add.textContent='';const inRoute=new Set(stops.map(s=>s.order_id));
  const free=orders.filter(o=>!o.route_id&&routable(o.status)&&!inRoute.has(o.id));
  if(!free.length)add.append(el('p','hint','Todas as entregas do dia já estão em rotas.'));
  free.forEach(o=>{const l=el('label','check');const c=el('input');c.type='checkbox';c.value=o.id;c.onchange=()=>{dirty=true};l.append(c,' '+o.place_name+' · '+o.due_at+(o.has_coordinates?'':' · sem coordenada'));add.append(l)})}}
async function saveStops(){
 const added=[...document.querySelectorAll('#routeAddList input:checked')].map(c=>Number(c.value));
 const res=await api('/api/routes/'+route.id+'/stops',{method:'PUT',body:JSON.stringify({order_ids:stops.map(s=>s.order_id).concat(added),version:route.version})});
 route.version=res.version;dirty=false;return res}
function routeAction(btn,fn,fallback){return()=>runLocked(btn,async()=>{setFeedback($('routeStatus'),'loading','Salvando…');
 try{const msg=await fn();if(msg===null)return setFeedback($('routeStatus'),'','');await loadAll();await openRoute(route.id,msg)}
 catch(err){setFeedback($('routeStatus'),'error',uiErrorMessage(err,fallback))}})}
async function optimize(keep){if(dirty||document.querySelector('#routeAddList input:checked'))await saveStops();
 const res=await api('/api/routes/'+route.id+'/optimize',{method:'POST',body:JSON.stringify({version:route.version,departure:$('routeDeparture').value||'07:00',keep_order:keep})});
 const warn=res.warnings.slice();if(res.source==='estimativa')warn.unshift('Sem OSRM: distâncias e horários estimados em linha reta.');
 warnings=warn;
 return 'Rota calculada: '+km(res.total_distance_m)+', '+minutes(res.total_duration_s)+' rodando.'}
if(canWrite){
 $('routeSave').onclick=routeAction($('routeSave'),async()=>{await saveStops();return 'Entregas da rota salvas.'},'Não foi possível salvar as entregas da rota.');
 $('routeOptimize').onclick=routeAction($('routeOptimize'),()=>optimize(false),'Não foi possível calcular a rota.');
 $('routeKeep').onclick=routeAction($('routeKeep'),()=>optimize(true),'Não foi possível calcular a rota.');
 $('routeRelease').onclick=routeAction($('routeRelease'),async()=>{if(dirty)await saveStops();
  await api('/api/routes/'+route.id+'/status',{method:'POST',body:JSON.stringify({status:'liberado',version:route.version})});return 'Rota liberada. O motorista já vê no celular.'},'Não foi possível liberar a rota.');
 $('routeCancel').onclick=routeAction($('routeCancel'),async()=>{if(!confirm('Cancelar esta rota? As entregas voltam a ficar sem rota.'))return null;
  await api('/api/routes/'+route.id+'/status',{method:'POST',body:JSON.stringify({status:'cancelado',version:route.version})});return 'Rota cancelada.'},'Não foi possível cancelar a rota.')}
$('routeClose').onclick=()=>$('routeDialog').close();

// Romaneio: uma folha por rota, com o que entregar em cada parada e espaço para assinatura.
$('routePrint').onclick=()=>{const r=route,w=window.open('','_blank');if(!w)return setFeedback($('routeStatus'),'error','O navegador bloqueou a janela de impressão. Libere pop-ups para este site.');
 const rows=r.stops.map(s=>'<tr><td>'+s.sequence+'</td><td><strong>'+esc(s.place_name)+'</strong><br>'+esc(s.address)+(s.contact?'<br>Contato: '+esc(s.contact):'')+(s.access_notes?'<br>Acesso: '+esc(s.access_notes):'')+'</td><td>'+(s.window_start?esc(s.window_start+'–'+s.window_end):'')+'</td><td>'+esc(s.eta||'')+'</td><td>'+(s.lines||[]).map(l=>esc(l.recipe_id?l.recipe_name+' · '+l.portions+' porções':l.item_name+' · '+displayDecimal(l.quantity)+(l.unit?' '+l.unit:''))).join('<br>')+'</td><td class="sig"></td></tr>').join('');
 w.document.write('<!doctype html><html lang="pt-BR"><head><meta charset="utf-8"><title>Romaneio rota '+r.id+'</title><style>body{font:13px system-ui,sans-serif;margin:24px;color:#111}h1{font-size:18px;margin:0 0 4px}p{margin:0 0 12px}table{width:100%;border-collapse:collapse}th,td{border:1px solid #999;padding:6px;vertical-align:top;text-align:left}th{background:#eee}.sig{width:22%}footer{margin-top:32px;display:flex;gap:48px}footer div{flex:1;border-top:1px solid #111;padding-top:4px}</style></head><body>'
  +'<h1>Romaneio · Rota #'+r.id+'</h1><p>'+esc(r.day.split('-').reverse().join('/'))+' · '+esc(r.vehicle_plate+' '+r.vehicle_description)+' · Motorista: '+esc(r.driver_name)+(r.total_distance_m!=null?' · '+esc(km(r.total_distance_m)):'')+'</p>'
  +'<table><thead><tr><th>#</th><th>Local</th><th>Janela</th><th>Previsão</th><th>Itens</th><th>Recebido por</th></tr></thead><tbody>'+rows+'</tbody></table>'
  +'<footer><div>Conferido na saída</div><div>Motorista</div></footer></body></html>');
 w.document.close();w.focus();w.print()};

// --- nova entrega e nova rota ---------------------------------------------------------
// Busca a cada formulário aberto: um veículo ou local cadastrado agora já aparece.
async function loadOptions(){options=await api('/api/dispatch/options');return options}
let orderOp=null;
function addLine(kind){const row=el('div','dispatch-line');row.dataset.kind=kind;
 const l1=el('label',null,kind==='meal'?'Refeição':'Insumo'),sel=el('select');sel.required=true;fillSelect(sel,kind==='meal'?options.recipes:options.items,'Escolha');l1.append(sel);
 const l2=el('label',null,kind==='meal'?'Porções':'Quantidade'),qty=el('input');qty.required=true;qty.inputMode=kind==='meal'?'numeric':'decimal';qty.autocomplete='off';l2.append(qty);
 const rm=el('button','secondary','Tirar');rm.type='button';rm.onclick=()=>row.remove();row.append(l1,l2,rm);$('orderLines').append(row);sel.focus()}
if(canWrite){
 $('orderNew').onclick=async()=>{try{await loadOptions()}catch(err){return setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar as opções. Atualize a página.'))}
  $('orderForm').reset();$('orderLines').textContent='';setFeedback($('orderFormStatus'),'','');orderOp=newOperationID();
  fillSelect($('orderPlace'),options.places,'Escolha o local');fillSelect($('orderWarehouse'),options.warehouses,'Escolha o depósito');
  $('orderDialog').showModal();$('orderPlace').focus()};
 $('addMeal').onclick=()=>addLine('meal');$('addItem').onclick=()=>addLine('item');
 $('orderFormCancel').onclick=()=>$('orderDialog').close();
 $('orderForm').addEventListener('submit',e=>{e.preventDefault();runLocked($('orderForm').querySelector('button[type=submit]'),async()=>{
  const fb=$('orderFormStatus');if(!requireFields($('orderForm'),fb))return;
  const lines=[...$('orderLines').children].map(row=>{const v=row.querySelector('select').value,q=row.querySelector('input').value.trim();
   return row.dataset.kind==='meal'?{recipe_id:Number(v),portions:Number(q)}:{item_id:Number(v),quantity:q}});
  if(!lines.length)return setFeedback(fb,'error','Inclua ao menos uma refeição ou um insumo.');
  setFeedback(fb,'loading','Criando a entrega…');
  try{await api('/api/delivery-orders',{method:'POST',body:JSON.stringify({operation_id:orderOp,place_id:Number($('orderPlace').value),origin_warehouse_id:Number($('orderWarehouse').value),day:day(),meal:$('orderMeal').value,due_at:$('orderDue').value,pod_required:$('orderPod').checked,notes:$('orderNotes').value.trim(),lines})});
   $('orderDialog').close();await loadAll();setFeedback(status,'success','Entrega criada. Coloque numa rota quando estiver pronta.')}
  catch(err){setFeedback(fb,'error',uiErrorMessage(err,'Não foi possível criar a entrega. Revise os campos e tente de novo.'))}})});
 $('routeNew').onclick=async()=>{try{await loadOptions()}catch(err){return setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar as opções. Atualize a página.'))}
  $('routeNewForm').reset();setFeedback($('routeNewStatus'),'','');fillSelect($('routeVehicle'),options.vehicles,'Escolha o veículo');fillSelect($('routeDriver'),options.drivers,'Escolha o motorista');
  $('routeNewDialog').showModal();$('routeVehicle').focus()};
 $('routeNewCancel').onclick=()=>$('routeNewDialog').close();
 $('routeNewForm').addEventListener('submit',e=>{e.preventDefault();runLocked($('routeNewForm').querySelector('button[type=submit]'),async()=>{
  const fb=$('routeNewStatus');if(!requireFields($('routeNewForm'),fb))return;
  try{const r=await api('/api/routes',{method:'POST',body:JSON.stringify({day:day(),vehicle_id:Number($('routeVehicle').value),driver_id:Number($('routeDriver').value)})});
   $('routeNewDialog').close();await loadAll();await openRoute(r.id,'Rota criada. Marque as entregas e salve.')}
  catch(err){setFeedback(fb,'error',uiErrorMessage(err,'Não foi possível criar a rota.'))}})})}
await loadAll();
})();
