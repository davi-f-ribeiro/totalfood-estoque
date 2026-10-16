// Tela do motorista. Cada ação vira uma operação com operation_id próprio, guardada
// no IndexedDB do aparelho e enviada em ordem quando houver conexão. O servidor
// ignora reenvios do mesmo operation_id, então tentar de novo é sempre seguro.
(async()=>{
const root=document.getElementById('driverRoot');if(!root)return;
const $=id=>document.getElementById(id);
const owner=(document.querySelector('meta[name="current-user-id"]')||{}).content||'';
const cacheKey='driver-route:'+owner,failKey='driver-failures:'+owner;
const stateLabel={programado:'Programada',despachado:'No caminhão',em_rota:'A caminho',chegou:'No local',entregue:'Entregue',falhou:'Não entregue',cancelado:'Cancelada'};
const mealLabel={cafe:'Café',almoco:'Almoço',lanche:'Lanche',jantar:'Jantar'};
const terminal=s=>s==='entregue'||s==='falhou'||s==='cancelado';
const nf=new Intl.NumberFormat('pt-BR',{maximumFractionDigits:3});
let token=csrf,data=null,current=null,syncing=false;

// --- fila no IndexedDB -------------------------------------------------------
let dbp=null;
function db(){if(!dbp)dbp=new Promise((res,rej)=>{const r=indexedDB.open('driver-queue',1);r.onupgradeneeded=()=>r.result.createObjectStore('ops',{keyPath:'seq',autoIncrement:true});r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error)});return dbp}
async function store(mode,fn){const d=await db();return new Promise((res,rej)=>{const tx=d.transaction('ops',mode),req=fn(tx.objectStore('ops'));tx.oncomplete=()=>res(req&&req.result);tx.onerror=()=>rej(tx.error)})}
const queueAll=async()=>((await store('readonly',s=>s.getAll()))||[]).filter(o=>o.owner===owner).sort((a,b)=>a.seq-b.seq);
const queueAdd=op=>store('readwrite',s=>s.add(Object.assign({owner,created:Date.now()},op)));
const queueDel=seq=>store('readwrite',s=>s.delete(seq));

function failures(){try{return JSON.parse(localStorage.getItem(failKey)||'[]')}catch(e){return[]}}
function addFailure(label,msg){try{let f=failures();f.push({label,msg});localStorage.setItem(failKey,JSON.stringify(f.slice(-10)))}catch(e){}renderFailures()}
function renderFailures(){const box=$('driverFailures'),f=failures();box.textContent='';if(!f.length){box.hidden=true;return}
 box.hidden=false;box.append(el('strong',null,'Não aceito pelo sistema. Avise o despacho:'));const ul=el('ul');f.forEach(x=>ul.append(el('li',null,x.label+': '+x.msg)));box.append(ul);
 const b=el('button','secondary','Entendi');b.type='button';b.onclick=()=>{try{localStorage.removeItem(failKey)}catch(e){}renderFailures()};box.append(b)}

// --- envio -------------------------------------------------------------------
async function freshToken(){const r=await fetch('/driver',{cache:'no-store',credentials:'same-origin'});const html=await r.text();const m=html.match(/name="csrf-token" content="([^"]*)"/);if(m)token=m[1];return !!m}
async function send(op,retry=true){
 let opt={method:'POST',credentials:'same-origin',headers:{'X-CSRF-Token':token}};
 if(op.form){const fd=new FormData();Object.entries(op.fields).forEach(([k,v])=>{if(v!=null&&v!=='')fd.append(k,v)});if(op.file)fd.append('file',op.file,'comprovante');opt.body=fd}
 else{opt.headers['Content-Type']='application/json';opt.body=JSON.stringify(op.body)}
 const r=await fetch(op.url,opt);const j=await r.json().catch(()=>({}));
 // Página aberta do cache traz um token antigo: pega um novo e tenta mais uma vez.
 if(r.status===403&&String(j.error||'').includes('Sua sessão expirou')&&retry&&await freshToken())return send(op,false);
 return {status:r.status,error:j.error||''}}
async function sync(){
 if(syncing)return;syncing=true;let sent=0;
 try{for(const op of await queueAll()){
  let res;try{res=await send(op)}catch(e){break}// sem rede: para e tenta depois
  if(res.status>=200&&res.status<300){await queueDel(op.seq);sent++;continue}
  if(res.status===401){setFeedback($('driverStatus'),'error','Sua sessão expirou. Entre de novo; as entregas ficam guardadas no aparelho.');break}
  if(res.status>=500)break;
  await queueDel(op.seq);addFailure(op.label,res.error||'erro '+res.status)}}
 finally{syncing=false}
 if(sent&&navigator.onLine)await loadRoute();else await renderBadge()}
async function renderBadge(){const n=(await queueAll()).length,b=$('syncBadge');
 b.className='driver-sync '+(n?'pending':'ok');b.textContent=n?n+(n===1?' envio pendente':' envios pendentes')+(navigator.onLine?'':' · sem conexão'):(navigator.onLine?'Tudo enviado':'Sem conexão')}
async function enqueue(op,apply){await queueAdd(op);apply();save();render();await renderBadge();sync()}

// --- rota local ----------------------------------------------------------------
function save(){try{localStorage.setItem(cacheKey,JSON.stringify(data))}catch(e){}}
function stopOf(id){return data&&data.route&&data.route.stops.find(s=>s.order_id===id)}
async function reapplyPending(){// o que ainda não subiu continua valendo na tela
 for(const op of await queueAll()){if(op.orderId&&op.status){const s=stopOf(op.orderId);if(s)s.status=op.status}
  if(op.routeStatus&&data.route){data.route.status=op.routeStatus;data.route.version=op.nextVersion}}}
async function loadRoute(){
 try{data=await api('/api/driver/route');await reapplyPending();save();setFeedback($('driverStatus'),'','')}
 catch(err){
  if(err.status===404){data=null;setFeedback($('driverStatus'),'error',uiErrorMessage(err));render();return}
  try{data=JSON.parse(localStorage.getItem(cacheKey)||'null')}catch(e){data=null}
  setFeedback($('driverStatus'),data?'loading':'error',data?'Sem conexão. Mostrando a rota salva no aparelho.':uiErrorMessage(err,'Não foi possível carregar a sua rota.'))}
 render();await renderBadge()}

// --- tela ----------------------------------------------------------------------
function el(tag,cls,text){const e=document.createElement(tag);if(cls)e.className=cls;if(text!=null)e.textContent=text;return e}
function mapURL(s){return s.latitude&&s.longitude?'https://www.google.com/maps/dir/?api=1&destination='+s.latitude+','+s.longitude:'https://www.google.com/maps/search/?api=1&query='+encodeURIComponent(s.address+(s.city?', '+s.city:''))}
function button(text,cls,fn){const b=el('button',cls,text);b.type='button';b.onclick=fn;return b}
function render(){
 const list=$('stopList');list.textContent='';$('routeActions').hidden=true;
 if(!data||!data.route){$('driverTitle').textContent='Minha rota';$('driverSubtitle').textContent=data?'Nenhuma rota liberada para você hoje.':'';return}
 const r=data.route,[y,m,d]=String(data.day).split('-');
 $('driverTitle').textContent='Minha rota · '+d+'/'+m;
 $('driverSubtitle').textContent=r.vehicle_plate+' · '+r.vehicle_description+' · '+r.stops.length+(r.stops.length===1?' entrega':' entregas');
 const allDone=r.stops.every(s=>terminal(s.status));
 $('routeActions').hidden=!(r.status==='liberado'||(r.status==='em_andamento'&&allDone));
 $('routeStart').hidden=r.status!=='liberado';$('routeFinish').hidden=!(r.status==='em_andamento'&&allDone);
 const next=r.stops.find(s=>!terminal(s.status));
 r.stops.forEach(s=>{
  const li=el('li','driver-stop'+(s===next&&r.status==='em_andamento'?' current':'')+(terminal(s.status)?' done':''));
  const top=el('div','driver-stop-top'),title=el('div');
  title.append(el('strong',null,s.place_name),el('small',null,s.address+(s.city?' · '+s.city:'')));
  top.append(el('span','driver-seq',String(s.sequence)),title,el('span','driver-state '+s.status,stateLabel[s.status]||s.status));
  const body=el('div','driver-stop-body'),info=[];
  if(s.eta)info.push('Previsão '+s.eta);info.push('Combinado '+s.due_at);if(s.window_start)info.push('Recebe '+s.window_start+'–'+s.window_end);
  body.append(el('small',null,info.join(' · ')));
  if(s.contact)body.append(el('small',null,'Contato: '+s.contact));if(s.access_notes)body.append(el('small',null,'Acesso: '+s.access_notes));
  const ul=el('ul');(s.lines||[]).forEach(l=>ul.append(el('li',null,l.recipe_id?(l.recipe_name+' · '+l.portions+' porções'):(l.item_name+' · '+nf.format(Number(l.quantity))+(l.unit?' '+l.unit:'')))));body.append(ul);
  li.append(top,body);
  if(r.status==='em_andamento'&&!terminal(s.status)){
   const act=el('div','actions');
   if(s===next){
   if(s.status==='despachado')act.append(button('A caminho',null,()=>event(s,'em_rota')));
   if(s.status==='em_rota')act.append(button('Cheguei',null,()=>event(s,'chegou')));
   if(s.status==='chegou')act.append(button('Entregar',null,()=>s.pod_required?openDeliver(s):event(s,'entregue')));
   if(s.status==='em_rota'||s.status==='chegou')act.append(button('Não entregue','secondary',()=>openFail(s)));}
   const a=el('a','button secondary','Abrir no mapa');a.href=mapURL(s);a.target='_blank';a.rel='noopener';act.append(a);
   li.append(act)}
  list.append(li)})}

function position(){return new Promise(res=>{if(!navigator.geolocation)return res({});
 navigator.geolocation.getCurrentPosition(p=>res({latitude:p.coords.latitude.toFixed(6),longitude:p.coords.longitude.toFixed(6)}),()=>res({}),{timeout:4000,maximumAge:60000,enableHighAccuracy:true})})}
async function event(s,status,notes){
 const pos=await position(),label=s.place_name+' ('+(stateLabel[status]||status)+')';
 const body=Object.assign({operation_id:newOperationID(),status,occurred_at:new Date().toISOString()},pos,notes?{notes}:{});
 await enqueue({url:'/api/driver/orders/'+s.order_id+'/events',body,label,orderId:s.order_id,status},()=>{s.status=status})}
function routeStatus(status){
 const r=data.route,label='Rota ('+(status==='em_andamento'?'início':'conclusão')+')',nextVersion=r.version+1;
 enqueue({url:'/api/driver/routes/'+r.id+'/status',body:{status,version:r.version},label,routeStatus:status,nextVersion},()=>{
  r.status=status;r.version=nextVersion;if(status==='em_andamento')r.stops.forEach(s=>{if(s.status==='programado')s.status='despachado'})})}
$('routeStart').onclick=()=>routeStatus('em_andamento');
$('routeFinish').onclick=()=>routeStatus('concluido');

// --- não entregue ----------------------------------------------------------------
function openFail(s){current=s;$('failForm').reset();$('failDialog').showModal();$('failReason').focus()}
$('failCancel').onclick=()=>$('failDialog').close();
$('failForm').addEventListener('submit',async e=>{e.preventDefault();if(!$('failForm').checkValidity())return $('failForm').reportValidity();
 const notes=[$('failReason').value,$('failNotes').value.trim()].filter(Boolean).join(' — ');$('failDialog').close();await event(current,'falhou',notes)});

// --- entrega com comprovante --------------------------------------------------------
const canvas=$('proofCanvas'),ctx=canvas.getContext('2d');let signed=false,drawing=false,photo=null;
function kind(){return document.querySelector('input[name="proofKind"]:checked').value}
function syncKind(){const k=kind();$('proofPhotoField').hidden=k!=='foto';$('proofSignField').hidden=k!=='assinatura';$('proofCodeField').hidden=k!=='codigo'}
document.querySelectorAll('input[name="proofKind"]').forEach(r=>r.addEventListener('change',syncKind));
function clearSign(){ctx.fillStyle='#fff';ctx.fillRect(0,0,canvas.width,canvas.height);ctx.lineWidth=3;ctx.lineCap='round';ctx.strokeStyle='#111';signed=false}
function point(e){const b=canvas.getBoundingClientRect();return[(e.clientX-b.left)*canvas.width/b.width,(e.clientY-b.top)*canvas.height/b.height]}
canvas.addEventListener('pointerdown',e=>{drawing=true;canvas.setPointerCapture(e.pointerId);ctx.beginPath();ctx.moveTo(...point(e))});
canvas.addEventListener('pointermove',e=>{if(!drawing)return;ctx.lineTo(...point(e));ctx.stroke();signed=true});
['pointerup','pointercancel'].forEach(t=>canvas.addEventListener(t,()=>{drawing=false}));
$('proofClear').onclick=clearSign;
// Foto do celular chega com vários MB: reduz para até 1600 px em JPEG antes de guardar.
async function shrink(file){try{const img=await createImageBitmap(file),k=Math.min(1,1600/Math.max(img.width,img.height));
 const c=document.createElement('canvas');c.width=Math.round(img.width*k);c.height=Math.round(img.height*k);c.getContext('2d').drawImage(img,0,0,c.width,c.height);
 return await new Promise(res=>c.toBlob(b=>res(b||file),'image/jpeg',.8))}catch(e){return file}}
$('proofPhoto').addEventListener('change',async()=>{const f=$('proofPhoto').files[0];photo=f?await shrink(f):null;const img=$('proofPreview');
 if(photo){img.src=URL.createObjectURL(photo);img.hidden=false}else img.hidden=true});
function openDeliver(s){current=s;$('deliverForm').reset();photo=null;$('proofPreview').hidden=true;clearSign();syncKind();setFeedback($('deliverStatus'),'','');
 $('proofTempField').hidden=!(s.lines||[]).some(l=>l.recipe_id);$('deliverDialog').showModal()}
$('deliverCancel').onclick=()=>$('deliverDialog').close();
$('deliverForm').addEventListener('submit',async e=>{e.preventDefault();const k=kind(),st=$('deliverStatus');
 let file=null;
 if(k==='foto'){if(!photo)return setFeedback(st,'error','Tire a foto da entrega.');if(photo.size>5*1024*1024)return setFeedback(st,'error','A foto passa de 5 MB. Tire de novo.');file=photo}
 if(k==='assinatura'){if(!signed)return setFeedback(st,'error','Peça a assinatura de quem recebe.');file=await new Promise(res=>canvas.toBlob(res,'image/png'))}
 if(k==='codigo'&&!$('proofCode').value.trim())return setFeedback(st,'error','Digite o código informado por quem recebe.');
 const s=current,pos=await position();$('deliverDialog').close();
 const fields=Object.assign({operation_id:newOperationID(),kind:k,receiver_name:$('proofReceiver').value.trim(),temperature_c:$('proofTemp').value.trim(),code:$('proofCode').value.trim(),occurred_at:new Date().toISOString()},pos);
 await enqueue({url:'/api/driver/orders/'+s.order_id+'/deliver',form:true,fields,file,label:s.place_name+' (entrega)',orderId:s.order_id,status:'entregue'},()=>{s.status='entregue'})});

window.addEventListener('online',sync);window.addEventListener('offline',renderBadge);
document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='visible')sync()});
setInterval(sync,30000);
if('serviceWorker' in navigator)navigator.serviceWorker.register('/driver-sw.js').catch(()=>{});
renderFailures();await loadRoute();sync();
})();
