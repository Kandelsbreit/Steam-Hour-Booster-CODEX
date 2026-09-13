'use strict';
const test=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),os=require('node:os'),path=require('node:path');
const {EventEmitter}=require('node:events');
const {Store}=require('../src/store');
const {Features,inSchedule,accrue,dayKey}=require('../src/features');
const {DEFAULTS,FeaturesStore}=require('../src/features-store');
const {exportBackup,unpack,restoreBackup,pack}=require('../src/backup');
const {Telegram}=require('../src/telegram');
function fixture(t){
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'agnia-features-'));t.after(()=>fs.rmSync(dir,{recursive:true,force:true}));
  const secure={isEncryptionAvailable:()=>true,encryptString:s=>Buffer.from('TEST-ENCRYPTED:'+s),decryptString:b=>b.toString().slice(15)};
  const store=new Store(dir,secure),clients=[];let now=new Date(2026,8,13,12).getTime(),network=true;
  class Client extends EventEmitter{constructor(){super();this.games=[];this.playingState={blocked:false};this.steamID='test';this.requests=0;}logOn(d){this.details=d;}logOff(){}gamesPlayed(ids,force){assert.notEqual(force,true);this.games.push([...ids]);}getUserOwnedApps(){this.requests++;return Promise.resolve({apps:[{appid:440,name:'TF2',playtime_forever:120}]});}requestFreeLicense(ids){return Promise.resolve({grantedAppIds:ids,grantedPackageIds:[]});}}
  const engine=new Features(store,()=>{const c=new Client();clients.push(c);return c;},()=>now,()=>network);
  const id=engine.add('test_account');engine.update(id,{appids:[440,570],batchSize:1,rotationMinutes:1,autoStart:true});
  const advance=ms=>{now+=ms;engine.tick();};
  const login=()=>{engine.start(id,'test-password');const c=clients.at(-1);c.emit('refreshToken','test-token');c.emit('loggedOn');advance(5000);return c;};
  return {dir,engine,store,id,clients,advance,login,setNetwork:v=>network=v,now:()=>now};
}
test('persistent per-game/day stats count active batches and exclude pauses and suspend gaps',async t=>{
  const f=fixture(t),c=f.login();await new Promise(setImmediate);f.advance(1000);f.engine.next(f.id);f.advance(1000);
  c.playingState.blocked=true;c.emit('playingState',true,730);f.advance(1000);f.advance(3600000);f.engine.flush();
  const d=new FeaturesStore(f.dir,[f.id]).account(f.id);assert.equal(d.activeMs,2000);assert.equal(d.gameMs,2000);assert.equal(d.games[440],1000);assert.equal(d.games[570],1000);
  assert.equal(d.days[dayKey(new Date(f.now()))].gameMs,2000);
});
test('statistics split exactly at local midnight',()=>{
  const a={activeMs:0,gameMs:0,days:{},games:{}};const midnight=new Date(2026,8,14).getTime();accrue(a,midnight-500,midnight+500,[440,570]);
  assert.equal(a.days['2026-09-13'].gameMs,1000);assert.equal(a.days['2026-09-14'].gameMs,1000);assert.equal(a.activeMs,1000);
});
test('overnight schedule uses starting weekday and exact end boundary',()=>{
  const o={...DEFAULTS,scheduleEnabled:true,scheduleDays:[1],scheduleStart:'22:00',scheduleEnd:'06:00'};
  assert.equal(inSchedule(new Date(2026,8,14,23).getTime(),o),true);
  assert.equal(inSchedule(new Date(2026,8,15,5,59).getTime(),o),true);
  assert.equal(inSchedule(new Date(2026,8,15,6).getTime(),o),false);
  assert.equal(inSchedule(new Date(2026,8,13,23).getTime(),o),false);
});
test('autostart waits for delay and network; manual stop cancels pending autostart',t=>{
  const f=fixture(t);f.store.saveToken(f.id,'test-token');f.setNetwork(false);f.engine.startup();f.advance(60000);assert.equal(f.clients.length,0);
  f.setNetwork(true);f.advance(1000);assert.equal(f.clients.length,1);f.engine.stop(f.id);f.engine.startup();f.engine.stop(f.id);f.advance(60000);assert.equal(f.clients.length,1);
});
test('network loss waits without making repeated clients',t=>{
  const f=fixture(t);f.store.saveToken(f.id,'test-token');f.setNetwork(false);f.engine.start(f.id);f.advance(30000);assert.equal(f.clients.length,0);
  f.setNetwork(true);f.advance(30000);assert.equal(f.clients.length,1);
});
test('schedule pauses game submission without disconnecting and resumes automatically',t=>{
  const f=fixture(t),c=f.login();f.engine.setOptions({...DEFAULTS,scheduleEnabled:true,scheduleDays:[],scheduleStart:'00:00',scheduleEnd:'00:00'});
  f.advance(1000);assert.deepEqual(c.games.at(-1),[]);assert.equal(f.engine.runtime(f.id).online,true);
  f.engine.setOptions(DEFAULTS);f.advance(1000);assert.deepEqual(c.games.at(-1),[440]);
  f.engine.stop(f.id);f.advance(1000);assert.equal(f.engine.runtime(f.id).desired,false);
});
test('breaks resume automatically; stop timer remains stopped',t=>{
  const f=fixture(t),c=f.login();f.engine.setOptions({...DEFAULTS,breakEvery:1,breakMinutes:1});
  for(let i=0;i<60;i++)f.advance(1000);assert.deepEqual(c.games.at(-1),[]);
  for(let i=0;i<60;i++)f.advance(1000);assert.ok(c.games.at(-1).length);
  f.engine.setOptions({...DEFAULTS,stopAfter:1});f.advance(1000);assert.equal(f.engine.runtime(f.id).desired,false);f.advance(100000);assert.equal(f.engine.runtime(f.id).desired,false);
});
test('local goals notify once and server goals use only server values',async t=>{
  const f=fixture(t);const messages=[];f.engine.on('alert',(m,kind)=>{if(kind==='goal')messages.push(m);});f.login();await new Promise(setImmediate);
  f.engine.goalSave(f.id,{appid:440,hours:1/3600,basis:'local'});f.advance(1000);f.advance(1000);assert.equal(messages.length,1);
  f.engine.goalSave(f.id,{appid:570,hours:0.0001,basis:'steam'});assert.equal(messages.length,1);
  f.engine.goalSave(f.id,{appid:440,hours:1,basis:'steam'});assert.equal(messages.length,2);f.advance(1000);assert.equal(messages.length,2);
});
test('presets can exceed 32 and are isolated per account',t=>{
  const f=fixture(t),second=f.engine.add('second_account');const ids=Array.from({length:65},(_,i)=>i+1);
  f.engine.presetSave(f.id,'65 games',ids);const p=f.engine.features.account(f.id).presets[0];f.engine.presetApply(f.id,p.id);
  assert.equal(f.engine.account(f.id).appids.length,65);assert.equal(f.engine.features.account(second).presets.length,0);
  assert.throws(()=>f.engine.presetApply(second,p.id));
});
test('library cache suppresses reconnect fetches and manual refresh is throttled',async t=>{
  const f=fixture(t),c=f.login();await new Promise(setImmediate);assert.equal(c.requests,1);
  f.engine.stop(f.id);f.engine.start(f.id);const c2=f.clients.at(-1);c2.emit('loggedOn');await new Promise(setImmediate);assert.equal(c2.requests,0);
  await assert.rejects(f.engine.refreshLibrary(f.id,true));f.advance(60000);await f.engine.refreshLibrary(f.id,true);assert.equal(c2.requests,1);
});
test('manual-only library mode makes no login requests and license requests are capped',async t=>{
  const f=fixture(t);f.engine.setOptions({...DEFAULTS,libraryHours:0});const c=f.login();assert.equal(c.requests,0);
  const result=await f.engine.freeLicense(f.id,[440]);assert.deepEqual(result.apps,[440]);await assert.rejects(f.engine.freeLicense(f.id,[440]));
});
test('hung login retries after watchdog timeout; guard input is not interrupted',t=>{
  const f=fixture(t);f.store.saveToken(f.id,'test-token');f.engine.start(f.id);f.advance(121000);assert.ok(f.engine.runtime(f.id).retryAt);
  f.advance(30000);assert.equal(f.clients.length,2);f.clients[1].emit('steamGuard',null,()=>{},false);f.advance(121000);assert.ok(f.engine.runtime(f.id).guard);
});
test('encrypted backup roundtrip, wrong password and tampering leave data intact',t=>{
  const f=fixture(t);f.store.saveToken(f.id,'SECRET-TOKEN');f.engine.presetSave(f.id,'Night',[440,570]);
  const file=exportBackup(f.engine,true,'strong password');assert.equal(file.includes(Buffer.from('SECRET-TOKEN')),false);
  assert.throws(()=>unpack(file,'bad'));const damaged=Buffer.from(file);damaged[damaged.length-1]^=1;assert.throws(()=>unpack(damaged,'strong password'));
  assert.equal(f.store.getToken(f.id),'SECRET-TOKEN');const data=unpack(file,'strong password');assert.equal(data.tokens[f.id],'SECRET-TOKEN');
  f.engine.presetDelete(f.id,f.engine.features.account(f.id).presets[0].id);restoreBackup(f.store,data);
  assert.equal(new FeaturesStore(f.dir,[f.id]).account(f.id).presets.length,1);assert.equal(f.store.getToken(f.id),'SECRET-TOKEN');
});
test('plaintext backup excludes tokens and unsafe account paths are rejected',t=>{
  const f=fixture(t);f.store.saveToken(f.id,'SECRET-TOKEN');const file=exportBackup(f.engine,false,'');const data=unpack(file);assert.deepEqual(data.tokens,{});
  assert.throws(()=>exportBackup(f.engine,true,''));data.config.accounts[0].id='../../bad';assert.throws(()=>unpack(pack(data)));
});
test('restore encryption failure leaves previous config and sessions intact',t=>{
  const f=fixture(t);f.store.saveToken(f.id,'OLD-TOKEN');const data=unpack(exportBackup(f.engine,true,'strong password'),'strong password');
  f.store.secure.isEncryptionAvailable=()=>false;assert.throws(()=>restoreBackup(f.store,data));assert.ok(f.store.hasToken(f.id));
  assert.equal(JSON.parse(fs.readFileSync(path.join(f.dir,'settings.json'))).accounts[0].name,'test_account');
});
test('Telegram rejects foreign chats, stale commands, and resumes only saved sessions',async t=>{
  const f=fixture(t);f.store.saveToken(f.id,'test-token');const sent=[];let calls=0;
  const updates=[
    {update_id:1,message:{chat:{id:999,type:'private'},from:{id:999},text:'/resume',date:f.now()/1000}},
    {update_id:2,message:{chat:{id:123,type:'private'},from:{id:123},text:'/resume',date:f.now()/1000-600}},
    {update_id:3,message:{chat:{id:123,type:'private'},from:{id:123},text:'/resume',date:f.now()/1000}}
  ];
  const tg=new Telegram(f.engine,async(url,body)=>{calls++;if(url.endsWith('getUpdates'))return {ok:true,result:updates};sent.push(body);return {ok:true,result:{}};});
  fs.writeFileSync(tg.file,f.store.secure.encryptString('123456:abcdefghijklmnopqrstuvwxyz'));
  Object.assign(tg.config,{enabled:true,chatId:'123',daily:false});await tg.poll(0);tg.stop();
  assert.equal(f.clients.length,1);assert.equal(sent.length,1);assert.equal(tg.config.offset,4);
  assert.equal(JSON.stringify(f.engine.snapshot()).includes('abcdefghijklmnopqrstuvwxyz'),false);assert.equal(calls,2);
});
