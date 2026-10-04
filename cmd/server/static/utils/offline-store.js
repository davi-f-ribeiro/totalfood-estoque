(function(global){
'use strict';

class OfflineStore{
  constructor(dbName='AppStore',storeName='pendingOps',ownerId=null){
    this.dbName=dbName;
    this.storeName=storeName;
    this.ownerId=String(ownerId??document.querySelector('meta[name="current-user-id"]')?.content??'').trim();
    this.dbPromise=null;
  }

  pendingId(kind){
    if(!this.ownerId)throw new TypeError('pending operation owner is required');
    if(!kind)throw new TypeError('pending operation kind is required');
    return this.ownerId+':'+kind;
  }

  init(){
    if(this.dbPromise)return this.dbPromise;
    this.dbPromise=new Promise((resolve,reject)=>{
      const request=indexedDB.open(this.dbName,1);
      request.onupgradeneeded=()=>{
        const db=request.result;
        if(!db.objectStoreNames.contains(this.storeName))db.createObjectStore(this.storeName,{keyPath:'id'});
      };
      request.onsuccess=()=>resolve(request.result);
      request.onerror=()=>reject(request.error);
    });
    return this.dbPromise;
  }

  async getPending(kind){
    const db=await this.init(),id=this.pendingId(kind);
    const record=await requestResult(db.transaction(this.storeName).objectStore(this.storeName).get(id));
    return record&&record.owner_id===this.ownerId&&record.kind===kind&&record.status==='pending'?record:null;
  }

  async claimPending(kind,operationId,endpoint,payload){
    if(!operationId)throw new TypeError('pending operation id is required');
    const db=await this.init(),id=this.pendingId(kind);
    return new Promise((resolve,reject)=>{
      const tx=db.transaction(this.storeName,'readwrite'),store=tx.objectStore(this.storeName);
      let result;
      tx.oncomplete=()=>resolve(result);
      tx.onerror=()=>reject(tx.error);
      tx.onabort=()=>reject(tx.error||new Error('pending operation transaction aborted'));
      const get=store.get(id);
      get.onerror=()=>reject(get.error);
      get.onsuccess=()=>{
        const current=get.result;
        if(current&&current.status==='pending'){
          result={claimed:current.operation_id===operationId,record:current};
          return;
        }
        const record={id,owner_id:this.ownerId,kind,operation_id:operationId,endpoint,payload,timestamp:Date.now(),status:'pending',last_error:''};
        const put=store.put(record);
        put.onerror=()=>reject(put.error);
        put.onsuccess=()=>{result={claimed:true,record}};
      };
    });
  }

  async clearPending(kind,operationId){
    if(!operationId)return false;
    const db=await this.init(),id=this.pendingId(kind);
    return new Promise((resolve,reject)=>{
      const tx=db.transaction(this.storeName,'readwrite'),store=tx.objectStore(this.storeName);
      let cleared=false;
      tx.oncomplete=()=>resolve(cleared);
      tx.onerror=()=>reject(tx.error);
      tx.onabort=()=>reject(tx.error||new Error('pending operation transaction aborted'));
      const get=store.get(id);
      get.onerror=()=>reject(get.error);
      get.onsuccess=()=>{
        const current=get.result;
        if(!current||current.operation_id!==operationId)return;
        const del=store.delete(id);
        del.onerror=()=>reject(del.error);
        del.onsuccess=()=>{cleared=true};
      };
    });
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
