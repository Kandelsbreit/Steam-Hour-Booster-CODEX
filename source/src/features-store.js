'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { atomicWrite } = require('./store');
const { parseIds } = require('./engine');

const DEFAULTS = Object.freeze({ startupDelay: 60, startMinimized: false, libraryHours: 24,
  scheduleEnabled: false, scheduleStart: '00:00', scheduleEnd: '00:00', scheduleDays: [0,1,2,3,4,5,6],
  breakEvery: 0, breakMinutes: 10, stopAfter: 0 });
const POPULAR = [
  [440,'Team Fortress 2'],[570,'Dota 2'],[730,'Counter-Strike 2'],[230410,'Warframe'],
  [1172470,'Apex Legends'],[238960,'Path of Exile'],[252950,'Rocket League'],[1085660,'Destiny 2'],
  [578080,'PUBG: BATTLEGROUNDS'],[236390,'War Thunder'],[444090,'Paladins'],[291550,'Brawlhalla'],
  [304930,'Unturned'],[105600,'Terraria'],[4000,"Garry's Mod"],[271590,'Grand Theft Auto V Legacy'],
  [730310,'DYNASTY WARRIORS 9'],[227300,'Euro Truck Simulator 2'],[413150,'Stardew Valley'],
  [1245620,'ELDEN RING'],[1091500,'Cyberpunk 2077'],[1938090,'Call of Duty'],[2357570,'Overwatch 2']
].map(([appid,name])=>({appid,name}));
const BUILTINS = [
  {id:'popular-free',name:'Популярные бесплатные',appids:[440,570,730,230410,1172470,238960,1085660,578080,236390,444090,291550,304930,2357570]},
  {id:'valve',name:'Valve',appids:[440,570,730,10,20,30,40,50,60,70,80,130,220,240,280,300,320,340,360,380,400,420,500,550,620]},
  {id:'cozy',name:'Спокойный вечер',appids:[105600,413150,227300,4000]}
];
function integer(v,min,max,label) { const n=Number(v); if (!Number.isInteger(n)||n<min||n>max) throw Error(label+': от '+min+' до '+max); return n; }
function time(v) { if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(v)) throw Error('Время должно быть ЧЧ:ММ'); return v; }
function options(v) {
  return { startupDelay:integer(v.startupDelay,0,3600,'Задержка запуска, с'),startMinimized:!!v.startMinimized,
    libraryHours:integer(v.libraryHours,0,720,'Обновление Steam, ч'),scheduleEnabled:!!v.scheduleEnabled,
    scheduleStart:time(v.scheduleStart),scheduleEnd:time(v.scheduleEnd),
    scheduleDays:[...new Set((Array.isArray(v.scheduleDays)?v.scheduleDays:[]).map(n=>integer(n,0,6,'День недели')))],
    breakEvery:integer(v.breakEvery,0,10080,'Работа до перерыва, мин'),breakMinutes:integer(v.breakMinutes,1,1440,'Перерыв, мин'),
    stopAfter:integer(v.stopAfter,0,43200,'Таймер остановки, мин') };
}
function defaults() { return {version:1,options:{...DEFAULTS},accounts:{},logs:[],notifications:[],
  telegram:{enabled:false,chatId:'',dailyTime:'21:00',daily:true,errors:true,offset:0,lastDaily:''}}; }
function accountData() { return {library:[],libraryAt:0,custom:[],presets:[],goals:[],days:{},games:{},activeMs:0,gameMs:0,licenseAt:0}; }
function finite(v) { if (typeof v!=='number'||!Number.isFinite(v)||v<0||v>1e18) throw Error('Повреждена статистика'); return v; }
function validate(data,ids) {
  if (!data||data.version!==1||!data.accounts||Array.isArray(data.accounts)) throw Error('Неверный формат данных функций');
  const out=defaults(); out.options=options({...DEFAULTS,...data.options});
  for (const id of ids) {
    const raw=data.accounts[id]; if (!raw) continue;
    const a=accountData(); a.activeMs=finite(raw.activeMs); a.gameMs=finite(raw.gameMs);
    a.libraryAt=finite(raw.libraryAt); a.licenseAt=finite(raw.licenseAt||0);
    for (const field of ['library','custom']) {
      if (!Array.isArray(raw[field])||raw[field].length>10000) throw Error('Слишком большая библиотека');
      a[field]=raw[field].map(g=>({appid:parseIds([g.appid])[0],name:String(g.name).slice(0,200),
        ...(field==='library'?{playtime_forever:finite(g.playtime_forever||0),playtime_2weeks:g.playtime_2weeks==null?null:finite(g.playtime_2weeks)}:{})}));
    }
    if (!Array.isArray(raw.presets)||raw.presets.length>100||!Array.isArray(raw.goals)||raw.goals.length>1000) throw Error('Слишком много пресетов или целей');
    a.presets=raw.presets.map(p=>({id:String(p.id).slice(0,64),name:String(p.name).slice(0,80),appids:parseIds(p.appids)}));
    a.goals=raw.goals.map(g=>({appid:parseIds([g.appid])[0],hours:finite(g.hours),basis:g.basis==='steam'?'steam':'local',notified:!!g.notified}));
    for (const [day,v] of Object.entries(raw.days||{})) {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) throw Error('Неверная дата статистики');
      a.days[day]={activeMs:finite(v.activeMs),gameMs:finite(v.gameMs)};
    }
    for (const [appid,ms] of Object.entries(raw.games||{})) a.games[parseIds([appid])[0]]=finite(ms);
    out.accounts[id]=a;
  }
  out.logs=(Array.isArray(data.logs)?data.logs:[]).slice(-500).map(l=>({time:finite(l.time),account:String(l.account).slice(0,64),message:String(l.message).slice(0,500)}));
  out.notifications=(Array.isArray(data.notifications)?data.notifications:[]).slice(-100).map(n=>({time:finite(n.time),message:String(n.message).slice(0,500)}));
  const tg=data.telegram||{};
  out.telegram={enabled:!!tg.enabled,chatId:/^-?\d{1,20}$/.test(tg.chatId)?String(tg.chatId):'',dailyTime:time(tg.dailyTime||'21:00'),
    daily:tg.daily!==false,errors:tg.errors!==false,offset:integer(tg.offset||0,0,Number.MAX_SAFE_INTEGER,'Telegram offset'),lastDaily:String(tg.lastDaily||'').slice(0,10)};
  return out;
}
class FeaturesStore {
  constructor(dir,ids) {
    this.file=path.join(dir,'features.json'); this.data=defaults();
    if(fs.existsSync(this.file)) this.data=validate(JSON.parse(fs.readFileSync(this.file,'utf8')),ids);
  }
  account(id) { return this.data.accounts[id] ||= accountData(); }
  save() { atomicWrite(this.file,JSON.stringify(this.data)); }
}
module.exports={FeaturesStore,DEFAULTS,POPULAR,BUILTINS,options,validate,defaults,accountData};
