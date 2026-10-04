(function(global){
'use strict';

class OfflineStore{
  constructor(dbName='AppStore',storeName='pendingOps',ownerId=null){
    this.dbName=dbName;
    this.storeName=storeName;
    this.ownerId=String(ownerId??document.querySelector('meta[name="current-user-id"]')?.content??'').trim();
    this.dbPromise=null;
  }

  pendingId(kind,operationId){
    if(!this.ownerId)throw new TypeError('pending operation owner is required');
    if(!kind)throw new TypeError('pending operation kind is required');
    if(!operationId)throw new TypeError('pending operation id is required');
    return this.ownerId+':'+kind+':'+operationId;
  }

  async savePending(kind,operationId,endpoint,payload,status='pending',lastError=''){
    return this.save({
      id:this.pendingId(kind,operationId),
      owner_id:this.ownerId,
      kind,
      operation_id:operationId,
      endpoint,
      payload,
      timestamp:Date.now(),
      status,
      last_error:lastError
    });
  }

  async listPending(kind){
    const rows=await this.getAll();
    return rows.filter(r=>r&&String(r.owner_id||'')===this.ownerId&&r.kind===kind&&r.status==='pending');
  }

  async removePending(kind,operationId){
    return this.remove(this.pendingId(kind,operationId));
  }

  async removeOwnerPending(){
    const rows=await this.getAll();
    for(const row of rows){
      if(row&&String(row.owner_id||'')===this.ownerId)await this.remove(row.id);
    }
  }

  init(){
    if(this.dbPromise)return this.dbPromise;
    this.dbPromise=new Promise((resolve,reject)=>{
      const request=indexedDB.open(this.dbName,1);
      request.onupgradeneeded=()=>{
        const db=request.result;
        if(!db.objectStoreNames.contains(this.storeName)){
          db.createObjectStore(this.storeName,{keyPath:'id'});
        }
      };
      request.onsuccess=()=>resolve(request.result);
      request.onerror=()=>reject(request.error);
    });
    return this.dbPromise;
  }

  async store(mode='readonly'){
    const db=await this.init();
    return db.transaction(this.storeName,mode).objectStore(this.storeName);
  }

  async save(record){
    if(!record||!record.id)throw new TypeError('pending operation id is required');
    const store=await this.store('readwrite');
    return requestResult(store.put(record));
  }

  async get(id){
    const store=await this.store();
    return requestResult(store.get(id));
  }

  async getAll(){
    const store=await this.store();
    return requestResult(store.getAll());
  }

  async remove(id){
    const store=await this.store('readwrite');
    await requestResult(store.delete(id));
  }
}

function requestResult(request){
  return new Promise((resolve,reject)=>{
    request.onsuccess=()=>resolve(request.result);
    request.onerror=()=>reject(request.error);
  });
}

global.OfflineStore=OfflineStore;
})(window);
