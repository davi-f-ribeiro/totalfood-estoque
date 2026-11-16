// Administração → Impressoras de etiquetas: criar acesso, listar e revogar.
// Usa as funções comuns de app.js (api, setFeedback, runLocked, requireFields,
// uiErrorMessage, uncertainWriteError).
(()=>{
'use strict';
const list=document.getElementById('adminPrintersList');
if(!list)return;
const form=document.getElementById('adminPrinterForm'),nameInput=document.getElementById('adminPrinterName');
const status=document.getElementById('adminPrinterStatus'),listStatus=document.getElementById('adminPrintersListStatus');
const tokenBox=document.getElementById('adminPrinterToken');

async function load(){
 const out=await api('/api/print/devices');
 const rows=out.devices||[];
 list.replaceChildren();
 if(!rows.length){
  const p=document.createElement('p');p.className='hint';p.textContent='Nenhuma impressora cadastrada.';
  list.append(p);return;
 }
 for(const d of rows){
  const row=document.createElement('div');row.className='row warehouse-row';
  const info=document.createElement('div'),name=document.createElement('strong'),meta=document.createElement('small');
  name.textContent=d.name;meta.textContent=(d.active?'Ativa':'Revogada')+seenLabel(d)+testLabel(d);
  info.append(name,meta);row.append(info);
  if(d.active){
   const actions=document.createElement('div');actions.className='printer-actions';
   const test=document.createElement('button');test.type='button';test.className='secondary';test.textContent='Enviar teste';
   test.addEventListener('click',()=>sendTest(d,test));
   const btn=document.createElement('button');btn.type='button';btn.className='danger';btn.textContent='Revogar';
   btn.addEventListener('click',()=>revoke(d,btn));
   actions.append(test,btn);row.append(actions);
  }
  list.append(row);
 }
}

// Situação do último teste de cada impressora, para conferir se ele saiu.
const testStates={PENDING:'aguardando o ESP32',CLAIMED:'sendo impresso',SENT:'enviado ao USB',
 UNCERTAIN:'sem confirmação do ESP32',FAILED:'falhou no ESP32',CANCELLED:'cancelado'};
// Última vez que o ESP32 buscou a fila (com ou sem etiqueta): mostra se ele alcança o servidor.
function seenLabel(d){
 if(!d.active)return '';
 return d.last_seen_at?' · Buscou a fila em '+d.last_seen_at:' · Ainda não buscou a fila';
}
function testLabel(d){
 if(!d.last_test_status)return '';
 return ' · Último teste '+(d.last_test_at||'')+': '+(testStates[d.last_test_status]||d.last_test_status);
}

async function sendTest(d,btn){
 await runLocked(btn,async()=>{
  setFeedback(listStatus,'loading','Enviando etiqueta de teste para '+d.name+'…');
  try{
   await api('/api/print/devices/'+d.id+'/test',{method:'POST',body:'{}'});
   setFeedback(listStatus,'success','Etiqueta de teste na fila de '+d.name+'. No ESP32, toque em Buscar e imprimir etiquetas.');
  }catch(err){
   setFeedback(listStatus,'error',uncertainWriteError(err)
    ?'Envio do teste sem confirmação. Atualize a página: se a impressora mostrar teste aguardando, ele está na fila.'
    :uiErrorMessage(err,'Não foi possível enviar o teste para '+d.name+'.'));
  }
  try{await load()}catch{}
 });
}

// Revogar pede um segundo toque, como as outras ações destrutivas do sistema.
async function revoke(d,btn){
 if(btn.dataset.confirm!=='1'){
  btn.dataset.confirm='1';btn.textContent='Confirmar revogação';
  setFeedback(listStatus,'','Revogar '+d.name+' corta o acesso na hora. Para voltar a usar a impressora, crie um acesso com outro nome. Toque de novo para confirmar.');
  return;
 }
 await runLocked(btn,async()=>{
  setFeedback(listStatus,'loading','Revogando '+d.name+'…');
  try{
   await api('/api/print/devices/'+d.id+'/revoke',{method:'POST',body:'{}'});
   setFeedback(listStatus,'success',d.name+' revogada. A impressora não busca mais etiquetas.');
   await load();
  }catch(err){
   setFeedback(listStatus,'error',uncertainWriteError(err)
    ?'Revogação de '+d.name+' sem confirmação. Atualize a página e confira a lista.'
    :uiErrorMessage(err,'Não foi possível revogar '+d.name+'.'));
  }
 });
}

// O código aparece uma única vez; recarregar a lista não pode apagá-lo.
function showToken(data){
 tokenBox.replaceChildren();
 const p=document.createElement('p');p.textContent='Código de acesso de '+data.name+'. Copie agora e digite nas Configurações do ESP32; ele não aparece de novo.';
 const code=document.createElement('code');code.textContent=data.token;
 const copy=document.createElement('button');copy.type='button';copy.className='secondary';copy.textContent='Copiar código';
 copy.addEventListener('click',async()=>{
  try{await navigator.clipboard.writeText(data.token);copy.textContent='Código copiado'}
  catch{copy.textContent='Selecione e copie o código acima'}
 });
 tokenBox.append(p,code,copy);tokenBox.hidden=false;
}

form.addEventListener('submit',async e=>{
 e.preventDefault();
 const submit=e.submitter||form.querySelector('button[type="submit"]');
 if(submit?.dataset.busy==='1')return;
 if(!requireFields(form,status,'Digite o nome da impressora.'))return;
 const name=nameInput.value.trim();
 await runLocked(submit,async()=>{
  setFeedback(status,'loading','Criando o acesso de '+name+'…');
  try{
   const data=await api('/api/print/devices',{method:'POST',body:JSON.stringify({name})});
   nameInput.value='';showToken(data);
   setFeedback(status,'success','Acesso de '+data.name+' criado.');
  }catch(err){
   if(uncertainWriteError(err))setFeedback(status,'error','Criação de '+name+' sem confirmação. Confira a lista: se ela aparecer, o código se perdeu; revogue e crie o acesso com outro nome.');
   else if(Number(err.status)===409)setFeedback(status,'error','Já existe uma impressora chamada '+name+', ativa ou revogada. Use outro nome.');
   else setFeedback(status,'error',uiErrorMessage(err,'Não foi possível criar o acesso de '+name+'.'));
  }
  try{await load()}catch(err){setFeedback(listStatus,'error',uiErrorMessage(err,'Não foi possível carregar as impressoras.'))}
 });
});

load().catch(err=>setFeedback(listStatus,'error',uiErrorMessage(err,'Não foi possível carregar as impressoras.')));
})();
