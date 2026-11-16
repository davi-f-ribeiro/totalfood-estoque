/* Início: o que precisa de atenção hoje. Os dados vêm de /api/stock; o servidor diz o que está abaixo do mínimo e quanto falta. */
(function(){
'use strict';
const box=document.getElementById('homeLow');if(!box)return;
const sel=document.getElementById('warehouse'),status=document.getElementById('homeStatus'),pendingBox=document.getElementById('homePending');
const canEntry=!!document.querySelector('.nav a[href="/entry"]');
function cell(role,cls){const d=document.createElement('div');d.setAttribute('role',role);if(cls)d.className=cls;return d}
function render(rows){
  box.textContent='';
  const low=rows.filter(r=>r.below_minimum);
  if(!low.length){const p=document.createElement('p');p.className='home-empty';p.textContent='Nenhum item abaixo do mínimo.';box.append(p);return}
  const head=cell('row','home-row home-row-head');
  [['Item',''],['Estoque','num'],['Mínimo','num'],['Falta','num'],['','']].forEach(([t,c])=>{const h=cell('columnheader',c);if(t)h.textContent=t;else{const s=document.createElement('span');s.className='sr-only';s.textContent='Ação';h.append(s)}head.append(h)});
  box.append(head);
  low.forEach(r=>{
    const row=cell('row','home-row'),name=cell('cell','home-name'),strong=document.createElement('strong');strong.textContent=r.name;name.append(strong);
    const q=cell('cell','num is-low');q.textContent=displayDecimal(r.quantity)+' '+r.unit;
    const m=cell('cell','num');m.textContent=displayDecimal(r.minimum)+' '+r.unit;
    const f=cell('cell','num is-short');f.textContent=displayDecimal(r.shortage)+' '+r.unit;
    const act=cell('cell','home-action');
    if(canEntry){const a=document.createElement('a');a.className='button secondary';a.href='/entry?item_id='+encodeURIComponent(r.item_id)+(sel.value?'&warehouse_id='+encodeURIComponent(sel.value):'');a.textContent='Dar entrada';act.append(a)}
    row.append(name,q,m,f,act);box.append(row);
  });
}
async function load(){
  setFeedback(status,'loading','Carregando…');box.textContent='';
  try{const rows=await api('/api/stock'+(sel.value?'?warehouse_id='+encodeURIComponent(sel.value):''));setFeedback(status,'','');render(rows)}
  catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar o estoque.'))}
}
async function init(){
  try{await loadWarehouses(true)}catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar os depósitos.'));return}
  sel.addEventListener('change',load);
  try{if(window.OfflineStore){const r=await new OfflineStore().getPending('movement');if(r)pendingBox.hidden=false}}catch(e){}
  await load();
}
init();
})();
