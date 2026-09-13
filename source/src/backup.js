'use strict';
const fs=require('node:fs');
const path=require('node:path');
const crypto=require('node:crypto');
const zlib=require('node:zlib');
const {settings}=require('./engine');
const {validate}=require('./features-store');
const {atomicWrite}=require('./store');
const MAGIC=Buffer.from('AGNIA1\n');
const LIMIT=32*1024*1024;
function pack(data,password='') {
  const payload=zlib.gzipSync(Buffer.from(JSON.stringify(data)));
  if(!password) return Buffer.concat([MAGIC,Buffer.from([0]),payload]);
  if(password.length<8) throw Error('Пароль резервной копии: минимум 8 символов');
  const salt=crypto.randomBytes(16),iv=crypto.randomBytes(12),key=crypto.scryptSync(password,salt,32);
  const cipher=crypto.createCipheriv('aes-256-gcm',key,iv); cipher.setAAD(MAGIC);
  const encrypted=Buffer.concat([cipher.update(payload),cipher.final()]); key.fill(0);
  return Buffer.concat([MAGIC,Buffer.from([1]),salt,iv,cipher.getAuthTag(),encrypted]);
}
function unpack(buffer,password='') {
  if(buffer.length>LIMIT||!buffer.subarray(0,MAGIC.length).equals(MAGIC)) throw Error('Это не резервная копия Agnia');
  let payload; const offset=MAGIC.length,mode=buffer[offset];
  try {
    if(mode===0) payload=buffer.subarray(offset+1);
    else if(mode===1) {
      if(!password) throw Error('Нужен пароль');
      const salt=buffer.subarray(offset+1,offset+17),iv=buffer.subarray(offset+17,offset+29),tag=buffer.subarray(offset+29,offset+45);
      const key=crypto.scryptSync(password,salt,32),cipher=crypto.createDecipheriv('aes-256-gcm',key,iv);
      cipher.setAAD(MAGIC); cipher.setAuthTag(tag);
      try { payload=Buffer.concat([cipher.update(buffer.subarray(offset+45)),cipher.final()]); } finally { key.fill(0); }
    } else throw Error('Версия не поддерживается');
    const data=JSON.parse(zlib.gunzipSync(payload,{maxOutputLength:LIMIT}).toString('utf8'));
    if(data.tokensIncluded&&mode!==1) throw Error('Сессии должны быть зашифрованы');
    return validateBackup(data);
  } catch { throw Error('Неверный пароль или повреждённая резервная копия. Данные не изменены.'); }
}
function validateBackup(data) {
  if(data?.version!==1||data.config?.version!==1||!Array.isArray(data.config.accounts)||data.config.accounts.length>3) throw Error('Неверная конфигурация');
  const names=new Set(),ids=new Set();
  const accounts=data.config.accounts.map(a=>{
    if(!/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(a.id)||ids.has(a.id)||
      !/^[a-zA-Z0-9_]{2,64}$/.test(a.name)||names.has(a.name.toLowerCase())) throw Error('Неверные аккаунты');
    ids.add(a.id); names.add(a.name.toLowerCase()); return {id:a.id,name:a.name,...settings(a)};
  });
  const tokens={};
  if(data.tokensIncluded) for(const id of ids) if(data.tokens?.[id]) {
    if(typeof data.tokens[id]!=='string'||data.tokens[id].length>16384) throw Error('Неверная сессия'); tokens[id]=data.tokens[id];
  }
  return {version:1,config:{version:1,autoLaunch:!!data.config.autoLaunch,accounts},features:validate(data.features,[...ids]),
    tokensIncluded:!!data.tokensIncluded,tokens};
}
function exportBackup(engine,includeTokens,password) {
  if(includeTokens&&String(password||'').length<8) throw Error('Для переноса токенов укажи пароль от 8 символов');
  engine.flush(); const tokens={};
  if(includeTokens) for(const a of engine.store.config.accounts) { const t=engine.store.getToken(a.id); if(t) tokens[a.id]=t; }
  const features=structuredClone(engine.features.data);
  // Telegram credentials and enabled state never travel in backups.
  features.telegram.enabled=false; features.telegram.offset=0;
  return pack({version:1,createdAt:new Date().toISOString(),config:engine.store.config,features,tokensIncluded:!!includeTokens,tokens},password);
}
function restoreBackup(store,data) {
  data=validateBackup(data);
  const dir=store.dir,stage=path.join(dir,'restore-'+crypto.randomUUID());
  fs.mkdirSync(stage,{recursive:true});
  const previous=path.join(stage,'previous'); fs.mkdirSync(previous);
  const entries=['settings.json','features.json','sessions'],moved=[],installed=[];
  let cleanup=false;
  try {
    atomicWrite(path.join(stage,'settings.json'),JSON.stringify(data.config,null,2));
    atomicWrite(path.join(stage,'features.json'),JSON.stringify(data.features));
    fs.mkdirSync(path.join(stage,'sessions'));
    for(const a of data.config.accounts) {
      let token=data.tokens[a.id];
      if(!data.tokensIncluded&&store.config.accounts.some(old=>old.id===a.id&&old.name===a.name)) token=store.getToken(a.id);
      if(token) {
        if(!store.secure.isEncryptionAvailable()) throw Error('Хранилище Windows недоступно');
        atomicWrite(path.join(stage,'sessions',a.id+'.dat'),store.secure.encryptString(token));
      }
    }
    for(const name of entries) {
      const target=path.join(dir,name);
      if(fs.existsSync(target)) { fs.renameSync(target,path.join(previous,name)); moved.push(name); }
      fs.renameSync(path.join(stage,name),target); installed.push(name);
    }
    store.config=data.config; cleanup=true;
  } catch(e) {
    try {
      for(const name of installed.reverse()) fs.rmSync(path.join(dir,name),{recursive:true,force:true});
      for(const name of moved.reverse()) fs.renameSync(path.join(previous,name),path.join(dir,name));
      cleanup=true;
    } catch {
      throw Error('Восстановление прервано. Исходные файлы сохранены в '+previous+'. Закрой программу и восстанови их вручную.');
    }
    throw e;
  } finally { if(cleanup) fs.rmSync(stage,{recursive:true,force:true}); }
}
module.exports={pack,unpack,exportBackup,restoreBackup,validateBackup,LIMIT};
