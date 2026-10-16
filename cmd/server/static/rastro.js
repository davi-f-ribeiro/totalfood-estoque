(function initRastroSettings(){
 const form=document.getElementById('rastroSettingsForm');if(!form)return;
 const $=id=>document.getElementById(id),status=$('rastroStatus'),sync=$('rastroSync'),company=$('rastroCompany'),login=$('rastroLogin'),password=$('rastroPassword');
 async function load(){
  const cfg=await api('/api/admin/rastroconnect');
  company.value=cfg.company||'';login.value=cfg.login||'';password.value='';
  sync.disabled=!cfg.configured||!cfg.key_ready;
  if(!cfg.key_ready)setFeedback(status,'error','Configure RASTROCONNECT_KEY no servidor para proteger a senha do rastreador.');
  else if(cfg.last_sync_at)setFeedback(status,'success','Última sincronização: '+new Date(cfg.last_sync_at).toLocaleString('pt-BR')+'.');
  else setFeedback(status,'',cfg.configured?'Credenciais salvas. Pode sincronizar a frota.':'Informe as credenciais para conectar a frota.');
 }
 form.addEventListener('submit',async e=>{
  e.preventDefault();const button=e.submitter||$('rastroSave');
  if(!form.checkValidity()){form.reportValidity();return}
  await runLocked(button,async()=>{
   setFeedback(status,'loading','Salvando acesso ao rastreador…');
   try{await api('/api/admin/rastroconnect',{method:'PUT',body:JSON.stringify({company:company.value.trim(),login:login.value.trim(),password:password.value})});
     password.value='';await load();setFeedback(status,'success','Credenciais protegidas e salvas. Agora sincronize a frota.');
   }catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível salvar o acesso ao rastreador.'))}
  });
 });
 sync.addEventListener('click',()=>runLocked(sync,async()=>{
  setFeedback(status,'loading','Consultando veículos e posições no Rastro Connect…');
  try{const result=await api('/api/admin/rastroconnect/sync',{method:'POST',body:'{}'});
   setFeedback(status,'success',result.received+' veículo(s) recebido(s); '+result.imported+' novo(s) cadastrado(s) como inativos; '+result.linked+' já existente(s) vinculado(s). Confira a capacidade dos novos veículos em Locais e frota.');
  }catch(err){setFeedback(status,'error',uiErrorMessage(err,'Não foi possível consultar o Rastro Connect. Confira o acesso.'))}
 }));
 load().catch(err=>setFeedback(status,'error',uiErrorMessage(err,'Não foi possível carregar a configuração do rastreamento.')));
})();
