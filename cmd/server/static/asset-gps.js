// Asset location is captured only on a user-initiated operation, never tracked in background.
window.AssetGPS = (() => {
 let selectedTag = '';
 let pending = null;
 const $ = id => document.getElementById(id);
 function feedback(message, error=false) {
  const node=$('assetGPSStatus');if(!node)return;
  node.hidden=!message;node.className='operation-feedback '+(error?'error':'success');node.textContent=message;
 }
 function setTag(tag){selectedTag=tag||'';refresh();}
 function locate() {
  return new Promise(resolve=>{
   if(!navigator.geolocation || !window.isSecureContext){feedback('GPS indisponível: use HTTPS e permita a localização.',true);return resolve(null)}
   navigator.geolocation.getCurrentPosition(p=>{
    const c=p.coords;
    if(!Number.isFinite(c.latitude)||!Number.isFinite(c.longitude)||!Number.isFinite(c.accuracy))return resolve(null);
    resolve({latitude:c.latitude,longitude:c.longitude,accuracy_m:c.accuracy,captured_at:new Date(p.timestamp).toISOString()});
   },()=>{feedback('Localização não autorizada ou indisponível. A operação pode continuar.',true);resolve(null)},
   {enableHighAccuracy:true,timeout:8000,maximumAge:0});
  });
 }
 async function beforeOperation(){pending=await locate();return pending}
 async function afterOperation(tag,source,operationId) {
  const position=pending;pending=null;
  if(!position||!tag)return;
  const payload={...position,source,operation_id:operationId||newOperationID()};
  // Retain a failed capture for retry rather than silently dropping an audit record.
  const key='assetGPSPending';
  try{sessionStorage.setItem(key,JSON.stringify({tag,payload}))}catch(_){}
  await submit(tag,payload);
 }
 async function submit(tag,payload){
  try {
   await api('/api/assets/'+encodeURIComponent(tag)+'/gps',{method:'POST',body:JSON.stringify(payload)});
   try{sessionStorage.removeItem('assetGPSPending')}catch(_){}
   feedback('Posição GPS registrada.');await refresh();
  }catch(e){feedback('Operação concluída, mas o GPS não foi registrado. Use Registrar posição atual ou tente novamente.',true)}
 }
 async function refresh(){
  const current=$('assetGPSCurrent'),list=$('assetGPSHistory');if(!current||!list)return;
  list.textContent='';
  if(!selectedTag){current.textContent='Selecione um patrimônio para consultar a posição.';return}
  try {
   const rows=await api('/api/assets/'+encodeURIComponent(selectedTag)+'/gps');
   current.textContent=rows.length?'Última posição: '+rows[0].latitude.toFixed(6)+', '+rows[0].longitude.toFixed(6)+' · precisão ±'+Math.round(rows[0].accuracy_m)+' m':'Nenhuma posição registrada.';
   rows.forEach(x=>{
    const row=document.createElement('div');row.className='row';
    const detail=document.createElement('span');
    detail.textContent=new Date(x.captured_at).toLocaleString('pt-BR')+' · '+x.source+' · '+x.user+' · ±'+Math.round(x.accuracy_m)+' m';
    const link=document.createElement('a');
    link.href='https://www.google.com/maps?q='+encodeURIComponent(x.latitude+','+x.longitude);
    link.target='_blank';link.rel='noopener noreferrer';link.textContent='Ver no mapa';
    row.append(detail,link);list.append(row);
   });
  }catch(e){current.textContent='Não foi possível consultar o histórico GPS.'}
 }
 async function manual(){
  if(!selectedTag){feedback('Selecione um patrimônio antes de registrar a posição.',true);return}
  const b=$('assetGPSManual');b.disabled=true;
  try{await beforeOperation();await afterOperation(selectedTag,'MANUAL')}finally{b.disabled=false}
 }
 async function retry(){
  let stored;try{stored=JSON.parse(sessionStorage.getItem('assetGPSPending')||'null')}catch(_){}
  if(stored?.tag&&stored?.payload)await submit(stored.tag,stored.payload);
 }
 document.addEventListener('DOMContentLoaded',()=>{
  const button=$('assetGPSManual');if(button)button.addEventListener('click',manual);
  retry();
 });
 return {setTag,beforeOperation,afterOperation};
})();
