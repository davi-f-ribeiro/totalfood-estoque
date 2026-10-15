(async()=>{
const root=document.getElementById('logisticsRoot');if(!root)return;
const canWrite=root.dataset.canWrite==='1',$=id=>document.getElementById(id);
const status=$('logisticsStatus'),coverage=$('coverageNotice'),placeSearch=$('placeSearch');
const kindLabel={cliente:'Cliente',fornecedor:'Fornecedor',deposito:'Depósito',outro:'Outro'};
const nf=new Intl.NumberFormat('pt-BR',{maximumFractionDigits:2});
const inputDec=v=>v==null?'':String(v).replace('.',',');
let places=[],vehicles=[],drivers=[],options=null;
function el(tag,cls,text){let e=document.createElement(tag);if(cls)e.className=cls;if(text!=null)e.textContent=text;return e}
function card(title,detail,inactive,onOpen){
 let b=el('button','catalog-parent-card');b.type='button';b.append(el('strong',null,title));
 let d=detail.filter(Boolean);if(inactive)d.push('Inativo');if(d.length)b.append(el('small',null,d.join(' · ')));
 if(canWrite)b.onclick=onOpen;else{b.classList.add('readonly');b.disabled=true}
 return b}
function fillSelect(sel,rows,emptyLabel,current){
 sel.textContent='';if(emptyLabel!=null){let o=el('option',null,emptyLabel);o.value='';sel.append(o)}
 rows.forEach(r=>{let o=el('option',null,r.name);o.value=r.id;sel.append(o)});sel.value=current?String(current):''}
function renderPlaces(){
 let list=$('placeList'),q=placeSearch.value.trim().toLowerCase();list.textContent='';
 let rows=places.filter(p=>!q||[p.name,p.customer_name,p.warehouse_name,p.city].some(v=>v&&v.toLowerCase().includes(q)));
 if(!rows.length){list.append(el('p','hint',places.length?'Nenhum local encontrado. Revise a busca.':'Nenhum local cadastrado.'));return}
 rows.forEach(p=>list.append(card(p.name,[kindLabel[p.kind]+(p.customer_name?': '+p.customer_name:p.warehouse_name?': '+p.warehouse_name:''),p.city,p.latitude==null?'Sem coordenada':null,p.window_start?'Recebe '+p.window_start+'–'+p.window_end:null],!p.active,()=>openPlace(p))))}
function renderVehicles(){
 let list=$('vehicleList');list.textContent='';
 if(!vehicles.length){list.append(el('p','hint','Nenhum veículo cadastrado.'));return}
 vehicles.forEach(v=>list.append(card(v.plate+' · '+v.description,[nf.format(Number(v.capacity_kg))+' kg',v.capacity_boxes?v.capacity_boxes+' caixas térmicas':null,v.refrigerated?'Refrigerado':null,v.asset_tag?'Patrimônio '+v.asset_tag:null],!v.active,()=>openVehicle(v))))}
function renderDrivers(){
 let list=$('driverList');list.textContent='';
 if(!drivers.length){list.append(el('p','hint','Nenhum motorista cadastrado.'));return}
 drivers.forEach(d=>list.append(card(d.name,[d.license_category?'CNH '+d.license_category:null,d.license_expired?'CNH vencida':null,d.default_vehicle_plate?'Veículo '+d.default_vehicle_plate:null,d.user_login?'Login '+d.user_login:null],!d.active,()=>openDriver(d))))}
function renderCoverage(missing){
 if(!missing.length){setFeedback(coverage,'','');return}
 let names=missing.slice(0,5).map(c=>c.name).join(', ')+(missing.length>5?' e mais '+(missing.length-5):'');
 setFeedback(coverage,'',missing.length+(missing.length===1?' cliente ativo está':' clientes ativos estão')+' sem local com coordenada: '+names+'.')}
async function loadAll(){
 setFeedback(status,'loading','Carregando locais e frota…');
 try{let cov;[places,vehicles,drivers,cov]=await Promise.all([api('/api/places'),api('/api/vehicles'),api('/api/drivers'),api('/api/places/coverage')]);renderCoverage(cov.customers_without_place||[])}
 catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar locais e frota. Atualize a página.'));return false}
 setFeedback(status,'','');renderPlaces();renderVehicles();renderDrivers();return true}
async function loadOptions(){if(!options)options=await api('/api/logistics/options');return options}
function wireDialog(prefix,label,collect,url,afterSave){
 const dlg=$(prefix+'Dialog'),form=$(prefix+'Form'),fStatus=$(prefix+'FormStatus');
 const close=()=>{form.hidden=true;if(dlg.open)dlg.close()};
 $(prefix+'Cancel').onclick=close;
 form.addEventListener('submit',async e=>{e.preventDefault();
  await runLocked(form.querySelector('button[type="submit"]'),async()=>{
   if(!requireFields(form,fStatus))return;
   let id=$(prefix+'Id').value,body=collect();if(id)body.version=Number(form.dataset.version);
   setFeedback(fStatus,'loading','Salvando '+label+'…');
   try{await api(id?url+'/'+id:url,{method:id?'PATCH':'POST',body:JSON.stringify(body)});
    close();options=null;await loadAll();setFeedback(status,'success',afterSave(body,!!id))}
   catch(err){setFeedback(fStatus,'error',uiErrorMessage(err,'Não foi possível salvar o '+label+'. Revise os campos e tente de novo.'))}})});
 return(row,title,createLabel,updateLabel,focusId)=>{form.reset();setFeedback(fStatus,'','');$(prefix+'Id').value=row?row.id:'';form.dataset.version=row?row.version:'';
  setCrudMode(form,!!row,createLabel,updateLabel);$(prefix+'DialogTitle').textContent=title;form.hidden=false;dlg.showModal();$(focusId).focus()}}
function syncPlaceKind(){let k=$('placeKind').value;$('placeCustomerField').hidden=k!=='cliente';$('placeWarehouseField').hidden=k!=='deposito';$('placeCustomer').required=k==='cliente';$('placeWarehouse').required=k==='deposito'}
let showPlace,showVehicle,showDriver;
async function withOptions(fn){try{await loadOptions()}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar as opções do formulário. Atualize a página.'));return}fn()}
function openPlace(p){withOptions(()=>{
 showPlace(p,p?'Editar local':'Novo local','Criar local','Atualizar local','placeName');
 fillSelect($('placeCustomer'),options.customers,'Escolha o cliente',p&&p.customer_id);fillSelect($('placeWarehouse'),options.warehouses,'Escolha o depósito',p&&p.warehouse_id);
 $('placeName').value=p?p.name:'';$('placeKind').value=p?p.kind:'cliente';$('placeAddress').value=p?p.address:'';$('placeCity').value=p&&p.city||'';
 $('placeLat').value=p?inputDec(p.latitude):'';$('placeLng').value=p?inputDec(p.longitude):'';$('placeWindowStart').value=p&&p.window_start||'';$('placeWindowEnd').value=p&&p.window_end||'';
 $('placeContact').value=p&&p.contact||'';$('placeAccess').value=p&&p.access_notes||'';$('placeActive').checked=p?p.active:true;syncPlaceKind()})}
function openVehicle(v){withOptions(()=>{
 showVehicle(v,v?'Editar veículo':'Novo veículo','Criar veículo','Atualizar veículo','vehiclePlate');
 fillSelect($('vehicleAsset'),options.assets,'Sem vínculo',v&&v.asset_id);
 $('vehiclePlate').value=v?v.plate:'';$('vehicleDescription').value=v?v.description:'';$('vehicleCapacityKg').value=v?inputDec(v.capacity_kg):'';$('vehicleBoxes').value=v&&v.capacity_boxes||'';
 $('vehicleRefrigerated').checked=!!(v&&v.refrigerated);$('vehicleActive').checked=v?v.active:true})}
function openDriver(d){withOptions(()=>{
 showDriver(d,d?'Editar motorista':'Novo motorista','Criar motorista','Atualizar motorista','driverName');
 fillSelect($('driverVehicle'),vehicles.filter(v=>v.active||(d&&v.id===d.default_vehicle_id)).map(v=>({id:v.id,name:v.plate+' · '+v.description})),'Nenhum',d&&d.default_vehicle_id);
 fillSelect($('driverUser'),options.users,'Sem login',d&&d.user_id);
 $('driverName').value=d?d.name:'';$('driverPhone').value=d&&d.phone||'';$('driverLicense').value=d&&d.license_number||'';$('driverCategory').value=d&&d.license_category||'';
 $('driverExpires').value=d&&d.license_expires_at||'';$('driverActive').checked=d?d.active:true})}
if(canWrite){
 const num=v=>Number(v)||0;
 showPlace=wireDialog('place','local',()=>({name:$('placeName').value.trim(),kind:$('placeKind').value,customer_id:num($('placeCustomer').value),warehouse_id:num($('placeWarehouse').value),address:$('placeAddress').value.trim(),city:$('placeCity').value.trim(),latitude:$('placeLat').value.trim(),longitude:$('placeLng').value.trim(),window_start:$('placeWindowStart').value,window_end:$('placeWindowEnd').value,contact:$('placeContact').value.trim(),access_notes:$('placeAccess').value.trim(),active:$('placeActive').checked}),'/api/places',(b,upd)=>'Local '+b.name+(upd?' atualizado.':' criado.'));
 showVehicle=wireDialog('vehicle','veículo',()=>({plate:$('vehiclePlate').value.trim(),description:$('vehicleDescription').value.trim(),asset_id:num($('vehicleAsset').value),capacity_kg:$('vehicleCapacityKg').value.trim(),capacity_boxes:num($('vehicleBoxes').value),refrigerated:$('vehicleRefrigerated').checked,active:$('vehicleActive').checked}),'/api/vehicles',(b,upd)=>'Veículo '+b.plate.toUpperCase()+(upd?' atualizado.':' criado.'));
 showDriver=wireDialog('driver','motorista',()=>({name:$('driverName').value.trim(),phone:$('driverPhone').value.trim(),license_number:$('driverLicense').value.trim(),license_category:$('driverCategory').value,license_expires_at:$('driverExpires').value,default_vehicle_id:num($('driverVehicle').value),user_id:num($('driverUser').value),active:$('driverActive').checked}),'/api/drivers',(b,upd)=>'Motorista '+b.name+(upd?' atualizado.':' criado.'));
 $('placeKind').addEventListener('change',syncPlaceKind);
 $('placeNew').onclick=()=>openPlace(null);$('vehicleNew').onclick=()=>openVehicle(null);$('driverNew').onclick=()=>openDriver(null)}
placeSearch.addEventListener('input',renderPlaces);
await loadAll();
})();
