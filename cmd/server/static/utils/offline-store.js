(function(global){
'use strict';

class OfflineStore{
  constructor(dbName='AppStore',storeName='pendingOps'){
    this.dbName=dbName;
    this.storeName=storeName;
    this.dbPromise=null;
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
