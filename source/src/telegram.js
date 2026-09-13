'use strict';
const fs=require('node:fs');
const path=require('node:path');
const {atomicWrite}=require('./store');
const {jsonRequest}=require('./http');
const {dayKey}=require('./features');
class Telegram {
  constructor(engine,request=jsonRequest) {
    this.engine=engine; this.request=request; this.file=path.join(engine.store.dir,'telegram.dat');
    this.timer=null; this.generation=0; this.running=false; this.failures=0; this.lastAlert=0;
    this.status='Выключен'; this.queue=[];
    engine.on('alert',(message,kind)=>{
      if(!this.config.enabled||(kind==='error'&&!this.config.errors)) return;
      if(kind==='error'&&engine.clock()-this.lastAlert<300000) return;
      this.lastAlert=engine.clock(); if(this.queue.length<20) this.queue.push(message);
    });
  }
  get config() { return this.engine.features.data.telegram; }
  hasToken() { return fs.existsSync(this.file); }
  token() { return this.hasToken()?this.engine.store.secure.decryptString(fs.readFileSync(this.file)):''; }
  save(input) {
    const token=String(input.token||'').trim(),chatId=String(input.chatId||'').trim();
    if(token&&!/^\d{5,20}:[A-Za-z0-9_-]{20,100}$/.test(token)) throw Error('Неверный токен Telegram-бота');
    if(chatId&&!/^\d{1,20}$/.test(chatId)) throw Error('Используй ID личного чата (положительное число)');
    if(!/^([01]\d|2[0-3]):[0-5]\d$/.test(input.dailyTime)) throw Error('Укажи время ежедневной сводки');
    if(input.enabled&&(!chatId||(!token&&!this.hasToken()))) throw Error('Укажи токен бота и ID личного чата');
    if(token) {
      if(!this.engine.store.secure.isEncryptionAvailable()) throw Error('Хранилище Windows недоступно');
      atomicWrite(this.file,this.engine.store.secure.encryptString(token));
    }
    const changed=!!token||chatId!==this.config.chatId;
    Object.assign(this.config,{enabled:!!input.enabled,chatId,dailyTime:input.dailyTime,daily:!!input.daily,errors:!!input.errors});
    if(changed) { this.config.offset=0; this.config.lastDaily=''; }
    this.engine.flush(); this.restart();
  }
  stop() { this.generation++; clearTimeout(this.timer); this.abort?.abort(); this.running=false; this.status='Выключен'; this.queue=[]; }
  restart() {
    this.stop(); if(!this.config.enabled) return;
    this.status='Подключение'; const gen=this.generation; this.abort=new AbortController();
    this.timer=setTimeout(()=>this.poll(gen),0);
  }
  async api(method,body) {
    const token=this.token(); if(!token) throw Error('Нет токена');
    const answer=await this.request(`https://api.telegram.org/bot${token}/${method}`,body,this.engine.bytes,60000,this.abort?.signal);
    if(!answer.ok) throw Error('Telegram отклонил запрос'); return answer.result;
  }
  report() {
    const today=dayKey(new Date(this.engine.clock()));
    return this.engine.snapshot().accounts.map(a=>`${a.name}: ${a.status}\nСегодня: ${((a.data.days[today]?.gameMs||0)/3600000).toFixed(2)} игровых ч; всего локально: ${(a.data.gameMs/3600000).toFixed(2)} ч\nАктивных игр: ${a.current.length}`).join('\n\n')||'Аккаунтов нет';
  }
  execute(text) {
    const [raw,name,...args]=String(text).trim().split(/\s+/); const command=raw.split('@')[0].toLowerCase();
    const all=this.engine.store.config.accounts;
    const accounts=!name||name==='all'?all:all.filter(a=>a.name.toLowerCase()===name.toLowerCase());
    if(['/pause','/resume','/next','/goal'].includes(command)&&!accounts.length) return 'Аккаунт не найден';
    switch(command) {
      case '/status': case '/report': return this.report();
      case '/pause': accounts.forEach(a=>this.engine.stop(a.id)); return 'Остановлено: '+accounts.map(a=>a.name).join(', ');
      case '/resume': {
        const result=[];
        for(const a of accounts) { try { if(!this.engine.runtime(a.id).desired) this.engine.start(a.id); result.push(a.name+': запуск'); } catch { result.push(a.name+': нужен вход в программе'); } }
        return result.join('\n');
      }
      case '/next': accounts.forEach(a=>this.engine.next(a.id)); return 'Следующая партия выбрана';
      case '/diagnostics': {
        const s=this.engine.snapshot(); return `Сеть: ${s.health.network?'есть':'нет'}; память: ${s.health.memoryMB} МБ\n`+s.accounts.map(a=>`${a.name}: ${a.status}; повтор: ${a.retryAt?new Date(a.retryAt).toLocaleTimeString('ru-RU'):'—'}`).join('\n');
      }
      case '/goals': return this.engine.snapshot().accounts.flatMap(a=>a.goals.map(g=>`${a.name} / ${g.appid}: ${g.minutes==null?'нет данных':(g.minutes/60).toFixed(2)} / ${g.hours} ч (${g.basis})`)).join('\n')||'Целей нет';
      case '/goal':
        if(accounts.length!==1||args.length<2) return '/goal логин AppID часы [local|steam]';
        this.engine.goalSave(accounts[0].id,{appid:args[0],hours:args[1],basis:args[2]}); return 'Цель сохранена';
      default: return '/status /report /diagnostics /goals\n/pause [логин|all]\n/resume [логин|all]\n/next [логин|all]\n/goal логин AppID часы [local|steam]';
    }
  }
  async poll(gen) {
    if(gen!==this.generation||!this.config.enabled) return;
    this.running=true; let delay=1000;
    try {
      const updates=await this.api('getUpdates',{offset:this.config.offset,timeout:50,allowed_updates:['message']});
      if(gen!==this.generation) return;
      for(const update of updates) {
        this.config.offset=Math.max(this.config.offset,update.update_id+1);
        // Persist before executing a mutation: a crash must not replay a remote command.
        this.engine.flush();
        const m=update.message;
        if(!m||String(m.chat?.id)!==this.config.chatId||m.chat?.type!=='private'||m.from?.is_bot||
          String(m.from?.id)!==this.config.chatId||!m.text||this.engine.clock()/1000-m.date>120) continue;
        let reply;
        try { reply=this.execute(m.text); } catch(e) { reply=e.message; }
        await this.api('sendMessage',{chat_id:this.config.chatId,text:reply.slice(0,3900)});
        if(gen!==this.generation) return;
      }
      if(this.queue.length) {
        await this.api('sendMessage',{chat_id:this.config.chatId,text:this.queue.slice(0,10).join('\n').slice(0,3900)}); this.queue.splice(0,10);
      }
      if(gen!==this.generation) return;
      const now=new Date(this.engine.clock()),today=dayKey(now),hm=String(now.getHours()).padStart(2,'0')+':'+String(now.getMinutes()).padStart(2,'0');
      if(this.config.daily&&this.config.lastDaily!==today&&hm>=this.config.dailyTime) {
        await this.api('sendMessage',{chat_id:this.config.chatId,text:('Ежедневная сводка\n'+this.report()).slice(0,3900)});
        this.config.lastDaily=today; this.engine.flush();
      }
      this.status='Подключён'; this.failures=0;
    } catch {
      this.failures++; delay=Math.min(900000,30000*2**Math.min(this.failures-1,5));
      this.status='Нет связи / проверь настройки. Повтор через '+Math.round(delay/1000)+' с';
    } finally {
      if(gen===this.generation) { this.running=false; this.timer=setTimeout(()=>this.poll(gen),delay); }
    }
  }
}
module.exports={Telegram};
