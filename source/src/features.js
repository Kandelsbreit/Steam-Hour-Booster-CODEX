'use strict';
const { randomUUID } = require('node:crypto');
const { Engine, parseIds } = require('./engine');
const { FeaturesStore, BUILTINS, POPULAR, options } = require('./features-store');

function dayKey(date) { return `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`; }
function inSchedule(now,o) {
  if(!o.scheduleEnabled) return true;
  const d=new Date(now), minutes=d.getHours()*60+d.getMinutes();
  const toMinutes=s=>Number(s.slice(0,2))*60+Number(s.slice(3));
  const start=toMinutes(o.scheduleStart),end=toMinutes(o.scheduleEnd);
  let weekday=d.getDay();
  if(start>end && minutes<end) weekday=(weekday+6)%7;
  return o.scheduleDays.includes(weekday) && (start===end || (start<end ? minutes>=start&&minutes<end : minutes>=start||minutes<end));
}
function accrue(a,from,to,ids) {
  let cursor=from;
  while(cursor<to) {
    const d=new Date(cursor), key=dayKey(d), midnight=new Date(d.getFullYear(),d.getMonth(),d.getDate()+1).getTime();
    const end=Math.min(to,midnight), ms=end-cursor;
    const day=a.days[key] ||= {activeMs:0,gameMs:0};
    day.activeMs+=ms; day.gameMs+=ms*ids.length; a.activeMs+=ms; a.gameMs+=ms*ids.length;
    for(const id of ids) a.games[id]=(a.games[id]||0)+ms;
    cursor=end;
  }
}
function bounded(promise,ms=30000) {
  let timer;
  return Promise.race([promise,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Время ожидания истекло')),ms);})]).finally(()=>clearTimeout(timer));
}
class Features extends Engine {
  constructor(store,createClient,clock,network=()=>true) {
    super(store,createClient,clock);
    this.features=new FeaturesStore(store.dir,store.config.accounts.map(a=>a.id));
    this.network=network; this.startedAt=this.clock(); this.lastSave=this.clock(); this.lastHeartbeat=this.clock();
    this.pending=new Map(); this.controls=new Map(); this.libraryBusy=new Map(); this.searchAt=0;
    this.logs=[...this.features.data.logs]; this.bytes={httpReceived:0,httpSent:0,requests:0};
    for(const a of store.config.accounts) this.runtime(a.id).library=this.features.account(a.id).library;
  }
  log(id,message) {
    super.log(id,message);
    if(this.features) {
      this.features.data.logs=this.logs.slice(-500);
      if(/отклонён|Нет связи|недоступн|Не удалось|завис|Сессия занята/.test(message)) this.emit('alert',`${id?this.account(id).name:'Программа'}: ${message}`,'error');
    }
  }
  notify(message) {
    this.features.data.notifications.push({time:this.clock(),message});
    this.features.data.notifications=this.features.data.notifications.slice(-100);
    this.emit('alert',message,'goal');
  }
  control(id) { return this.controls.get(id) || {startedAt:this.clock(),workMs:0,breakUntil:0}; }
  start(id,password) {
    this.pending.delete(id);
    if(!this.runtime(id).desired) this.controls.set(id,{startedAt:this.clock(),workMs:0,breakUntil:0});
    super.start(id,password);
  }
  connect(id,password) {
    if(!password&&!this.network()) {
      const r=this.runtime(id); this.detach(r); r.retryAt=this.clock()+30000; r.status='Ожидание сети / VPN'; return;
    }
    super.connect(id,password);
    this.runtime(id).connectingAt=this.clock();
  }
  stop(id) { this.pending?.delete(id); this.controls?.delete(id); super.stop(id); }
  remove(id) { super.remove(id); delete this.features.data.accounts[id]; this.flush(); }
  startup() {
    let n=0;
    for(const a of this.store.config.accounts) if(a.autoStart&&this.store.hasToken(a.id)) {
      this.pending.set(a.id,this.clock()+this.features.data.options.startupDelay*1000+n++*10000);
      this.runtime(a.id).status='Ожидание автозапуска / сети';
    }
  }
  startAll() {
    const failures=[];
    for(const a of this.store.config.accounts) if(!this.runtime(a.id).desired) {
      try { this.start(a.id); } catch { failures.push(a.name); }
    }
    if(failures.length) throw Error('Нужен первый или повторный вход: '+failures.join(', '));
  }
  pauseReason(id) {
    const now=this.clock(),o=this.features.data.options,c=this.control(id);
    if(c.breakUntil>now) return `Перерыв до ${new Date(c.breakUntil).toLocaleTimeString('ru-RU')}`;
    if(!inSchedule(now,o)) return 'Пауза по расписанию';
    return '';
  }
  apply(id) {
    if(this.features) {
      const r=this.runtime(id), reason=this.pauseReason(id);
      if(reason&&r.desired) { this.clearGames(r); r.status=reason; return; }
    }
    super.apply(id);
  }
  tick() {
    const now=this.clock(),o=this.features.data.options;
    for(const [id,at] of this.pending) if(now>=at&&this.network()) {
      this.pending.delete(id);
      try { this.start(id); } catch { this.log(id,'Автовход недоступен: нужен повторный вход'); }
    }
    for(const a of this.store.config.accounts) {
      const r=this.runtime(a.id),c=this.control(a.id),raw=now-r.lastTick;
      if(raw>=0&&raw<5000&&r.online&&r.desired&&!r.blocked&&r.sent.length) {
        accrue(this.features.account(a.id),r.lastTick,now,r.sent); c.workMs+=raw;
      }
      if(r.desired&&o.stopAfter&&now-c.startedAt>=o.stopAfter*60000) { this.stop(a.id); this.log(a.id,'Остановлено по таймеру'); continue; }
      if(c.breakUntil&&now>=c.breakUntil) { c.breakUntil=0; c.workMs=0; }
      if(r.desired&&o.breakEvery&&c.workMs>=o.breakEvery*60000&&!c.breakUntil) {
        c.breakUntil=now+o.breakMinutes*60000; this.log(a.id,'Начался запланированный перерыв');
      }
      if(r.client&&!r.online&&!r.guard&&now-r.connectingAt>120000) {
        this.log(a.id,'Подключение зависло: повтор с задержкой'); this.failure(a.id,3);
      }
    }
    super.tick();
    for(const a of this.store.config.accounts) {
      this.checkGoals(a.id);
      const d=this.features.account(a.id),r=this.runtime(a.id);
      if(o.libraryHours&&r.online&&now-Math.max(d.libraryAt,d.libraryAttemptAt||0)>=o.libraryHours*3600000) {
        this.refreshLibrary(a.id).catch(()=>this.live.has(a.id)&&this.log(a.id,'Обновление Steam недоступно; сохранён локальный кеш'));
      }
    }
    this.lastHeartbeat=now;
    if(now-this.lastSave>=30000) this.flush();
  }
  flush() { this.features.data.logs=this.logs.slice(-500); this.features.save(); this.lastSave=this.clock(); }
  async refreshLibrary(id,force=false) {
    const a=this.features?.account(id);
    if(!a) return super.refreshLibrary(id);
    const now=this.clock(),hours=this.features.data.options.libraryHours;
    if(!force&&((a.libraryAt&&(hours===0||now-a.libraryAt<hours*3600000))||a.libraryAttemptAt&&now-a.libraryAttemptAt<900000)) {
      this.runtime(id).library=a.library; return;
    }
    if(!force&&hours===0) { this.runtime(id).library=a.library; return; }
    if(this.libraryBusy.has(id)) return this.libraryBusy.get(id);
    if(force&&a.libraryAttemptAt&&now-a.libraryAttemptAt<60000) throw Error('Обновление доступно раз в минуту');
    a.libraryAttemptAt=now;
    const client=this.runtime(id).client;
    // Base implementation is retained; only cache, cadence and duplicate requests are added.
    const work=bounded(super.refreshLibrary(id)).then(()=>{
      if(this.live.has(id)&&this.runtime(id).client===client) {
        a.library=this.runtime(id).library; a.libraryAt=this.clock(); this.checkGoals(id); this.flush();
      }
    }).finally(()=>this.libraryBusy.delete(id));
    this.libraryBusy.set(id,work); return work;
  }
  setOptions(input) { this.features.data.options=options(input); this.flush(); }
  presetSave(id,name,appids) {
    this.account(id); const a=this.features.account(id); name=String(name||'').trim().slice(0,80);
    if(!name) throw Error('Укажи название пресета');
    if(a.presets.length>=100) throw Error('Максимум 100 собственных пресетов');
    a.presets.push({id:randomUUID(),name,appids:parseIds(appids)}); this.flush();
  }
  presetDelete(id,preset) { this.account(id); const a=this.features.account(id); a.presets=a.presets.filter(p=>p.id!==preset); this.flush(); }
  presetApply(id,preset) {
    const p=[...BUILTINS,...this.features.account(id).presets].find(p=>p.id===preset);
    if(!p) throw Error('Пресет не найден'); this.update(id,{...this.account(id),appids:p.appids});
  }
  customAdd(id,appid,name) {
    this.account(id); appid=parseIds([appid])[0]; const a=this.features.account(id);
    if(a.custom.length>=10000) throw Error('Максимум 10 000 игр');
    a.custom=a.custom.filter(g=>g.appid!==appid); a.custom.push({appid,name:String(name||`App ${appid}`).slice(0,200)}); this.flush();
  }
  gameRemove(id,appid) {
    appid=parseIds([appid])[0]; const a=this.account(id),d=this.features.account(id);
    d.custom=d.custom.filter(g=>g.appid!==appid);
    this.update(id,{...a,appids:a.appids.filter(n=>n!==appid)}); this.flush();
  }
  async freeLicense(id,appids) {
    const ids=parseIds(appids),r=this.runtime(id),d=this.features.account(id);
    if(!r.online) throw Error('Сначала войди в аккаунт');
    if(!ids.length||ids.length>32) throw Error('Выбери от 1 до 32 игр для запроса лицензий');
    if(d.licenseAt&&this.clock()-d.licenseAt<3600000) throw Error('Запрос бесплатных лицензий доступен раз в час');
    d.licenseAt=this.clock(); this.flush();
    const result=await bounded(r.client.requestFreeLicense(ids));
    if(this.live.has(id)) this.log(id,'Бесплатные лицензии: выдано игр '+(result.grantedAppIds?.length||0)+', пакетов '+(result.grantedPackageIds?.length||0));
    return {apps:result.grantedAppIds||[],packages:result.grantedPackageIds||[]};
  }
  goalSave(id,input) {
    this.account(id); const appid=parseIds([input.appid])[0],hours=Number(input.hours),a=this.features.account(id);
    if(!Number.isFinite(hours)||hours<=0||hours>1000000) throw Error('Цель: от 0 до 1 000 000 часов (не включая 0)');
    if(a.goals.length>=1000&&!a.goals.some(g=>g.appid===appid)) throw Error('Максимум 1000 целей');
    a.goals=a.goals.filter(g=>g.appid!==appid); a.goals.push({appid,hours,basis:input.basis==='steam'?'steam':'local',notified:false});
    this.checkGoals(id); this.flush();
  }
  goalDelete(id,appid) { this.account(id); const d=this.features.account(id); d.goals=d.goals.filter(g=>g.appid!==Number(appid)); this.flush(); }
  goalProgress(id,g) {
    const a=this.features.account(id);
    return g.basis==='steam' ? (a.library.find(n=>n.appid===g.appid)?.playtime_forever??null) : (a.games[g.appid]||0)/60000;
  }
  checkGoals(id) {
    for(const g of this.features.account(id).goals) if(!g.notified&&this.goalProgress(id,g)!==null&&this.goalProgress(id,g)>=g.hours*60) {
      g.notified=true; this.notify(`${this.account(id).name}: цель ${g.hours} ч для App ${g.appid} достигнута (${g.basis==='steam'?'Steam':'локальный счётчик'})`);
    }
  }
  snapshot() {
    const s=super.snapshot(); if(!this.features) return s;
    s.version='1.1.0'; s.options=this.features.data.options; s.presets=BUILTINS; s.popular=POPULAR;
    s.notifications=this.features.data.notifications; s.telegram={...this.features.data.telegram};
    s.health={uptimeMs:Math.max(0,this.clock()-this.startedAt),heartbeat:this.lastHeartbeat,network:this.network(),bytes:this.bytes,
      memoryMB:Math.round(process.memoryUsage().rss/1048576)};
    for(const a of s.accounts) {
      const d=this.features.account(a.id),r=this.runtime(a.id);
      a.data=d; a.pendingAt=this.pending.get(a.id)||0; a.retryAt=r.retryAt; a.failures=r.failures;
      a.nextBatches=Array.from({length:Math.ceil(a.appids.length/a.batchSize)},(_,i)=>({index:i,appids:a.appids.slice(i*a.batchSize,(i+1)*a.batchSize)}));
      a.goals=d.goals.map(g=>({...g,minutes:this.goalProgress(a.id,g)}));
      a.stopAt=this.features.data.options.stopAfter&&r.desired?this.control(a.id).startedAt+this.features.data.options.stopAfter*60000:0;
    }
    return s;
  }
}
module.exports={Features,dayKey,inSchedule,accrue,bounded};
