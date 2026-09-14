'use strict';
const $=id=>document.getElementById(id);
const el=(tag,text,cls)=>{const e=document.createElement(tag); if(text!==undefined)e.textContent=text;if(cls)e.className=cls;return e;};
const btn=(text,action,cls='secondary small')=>{const b=el('button',text,cls);b.type='button';b.onclick=action;return b;};
const hours=ms=>(ms/3600000).toLocaleString('ru-RU',{minimumFractionDigits:2,maximumFractionDigits:2});
const duration=ms=>{const s=Math.max(0,Math.floor(ms/1000));return `${Math.floor(s/3600)}:${String(Math.floor(s/60)%60).padStart(2,'0')}:${String(s%60).padStart(2,'0')}`;};
const stamp=ms=>ms?new Date(ms).toLocaleString('ru-RU'):'ещё не загружены';
const dateKey=d=>`${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;
let state={accounts:[],logs:[],presets:[],popular:[]},selected=null,page='boost',dirty=false,formsReady=false,optionsKey='',libraryKey='',navKey='',presetKey='',overviewKey='',queueKey='';
const titles={boost:['Пусть часы идут.','Аккаунты, текущие партии и автоматическое восстановление.'],games:['Твоя библиотека.','Выбирай игры. Остальное сделает очередь.'],presets:['Набор на любой день.','Готовые и собственные пресеты выбранного аккаунта.'],stats:['Каждый час на виду.','История сохраняется между запусками программы.'],diagnostics:['Всё ли в порядке?','Состояние, причины остановок и журнал работы.'],telegram:['Всегда под рукой.','Статус и управление из твоего личного чата.'],settings:['В твоём ритме.','Автоматизация, экономия трафика и резервные копии.']};
function notice(text){$('notice').textContent=text;$('notice').hidden=!text;}
async function command(name,payload={}){
  try {const answer=await window.steamHours.command(name,payload);if(!answer.ok){notice(answer.error);return null;}if(answer.state)render(answer.state);return answer;}
  catch{notice('Связь с программой потеряна. Закрой и открой окно из трея.');return null;}
}
function currentAccount(){return state.accounts.find(a=>a.id===selected);}
function selectedIds(){return [...new Set($('appids').value.split(/[\s,;]+/).filter(Boolean))];}
function fill(a,clearSecrets=false){
  $('appids').value=a.appids.join(', ');$('batch-size').value=a.batchSize;$('rotation').value=a.rotationMinutes;$('auto-start').checked=a.autoStart;
  if(clearSecrets){$('password').value='';$('guard-code').value='';$('search').value='';$('search-results').replaceChildren();}
  dirty=false;libraryKey='';$('dirty').textContent='';$('selection-count').textContent=a.appids.length+' игр';
}
function select(id){if(id===selected)return;if(dirty&&!confirm('Несохранённый выбор игр будет сброшен. Переключить аккаунт?'))return;selected=id;fill(currentAccount(),true);notice('');render(state);}
function switchPage(name){page=name;document.querySelectorAll('[data-page]').forEach(b=>{b.classList.toggle('selected',b.dataset.page===page);b.setAttribute('aria-current',b.dataset.page===page?'page':'false');});$('page-title').textContent=titles[page][0];$('page-subtitle').textContent=titles[page][1];render(state);}
function markDirty(){dirty=true;libraryKey='';$('dirty').textContent='Есть несохранённые изменения';$('selection-count').textContent=selectedIds().length+' игр';}
function allGames(a){
  const known=new Map((state.popular||[]).map(g=>[g.appid,g]));
  const map=new Map(a.data.custom.map(g=>[g.appid,g]));
  for(const g of a.library)map.set(g.appid,g);
  for(const id of [...a.appids,...selectedIds().map(Number).filter(n=>Number.isSafeInteger(n)&&n>0)])if(!map.has(id))map.set(id,known.get(id)||{appid:id,name:'App '+id});
  return [...map.values()].sort((a,b)=>a.name.localeCompare(b.name));
}
function visibleGames(a){const q=$('search').value.trim().toLowerCase();return allGames(a).filter(g=>g.name.toLowerCase().includes(q)||String(g.appid).includes(q));}
function gameName(a,id){return a.library.find(g=>g.appid===id)?.name||a.data.custom.find(g=>g.appid===id)?.name||state.popular.find(g=>g.appid===id)?.name||'App '+id;}
function renderLibrary(a){
  const key=JSON.stringify([a.id,a.library,a.data.custom,$('search').value,$('appids').value]);if(key===libraryKey)return;libraryKey=key;
  const visible=visibleGames(a),ids=new Set(selectedIds()),fragment=document.createDocumentFragment();
  $('library-count').textContent=`· ${allGames(a).length}`;$('library-updated').textContent='Серверные данные: '+stamp(a.data.libraryAt);
  for(const g of visible.slice(0,200)){
    const row=el('div',undefined,'game-row'),label=el('label'),check=el('input');check.type='checkbox';check.checked=ids.has(String(g.appid));
    check.onchange=()=>{const ids=new Set(selectedIds());if(check.checked)ids.add(String(g.appid));else ids.delete(String(g.appid));$('appids').value=[...ids].join(', ');markDirty();};
    label.append(check,el('span',g.name),el('small',`#${g.appid} · Steam: ${g.playtime_forever==null?'—':(g.playtime_forever/60).toFixed(1)+' ч'}`));
    row.append(label,btn('Убрать',async()=>{const ids=selectedIds().filter(n=>n!==String(g.appid));$('appids').value=ids.join(', ');markDirty();if(a.data.custom.some(x=>x.appid===g.appid))await command('custom-remove',{id:a.id,appid:g.appid});renderLibrary(currentAccount());}));fragment.append(row);
  }
  if(!visible.length)fragment.append(el('p','Здесь пока пусто. Добавь AppID, популярные игры или загрузи библиотеку Steam.','muted'));
  if(visible.length>200)fragment.append(el('p',`Показано 200 из ${visible.length}. Уточни поиск. «Выбрать все» добавляет все найденные игры.`,'muted'));
  $('library-list').replaceChildren(fragment);
}
function renderBoost(a){
  const key=JSON.stringify(state.accounts.map(a=>[a.id,a.name,a.status,a.desired,a.pendingAt]));
  if(key!==overviewKey){overviewKey=key;$('overview').replaceChildren(...state.accounts.map(a=>{
    const card=el('div',undefined,'panel overview-card');card.append(el('h2',a.name),el('p',a.status,'muted'));
    const row=el('div',undefined,'row');row.append(btn('Открыть',()=>select(a.id)),btn(a.desired||a.pendingAt?'Стоп':'Запуск',()=>command(a.desired||a.pendingAt?'stop':'start',{id:a.id})));card.append(row);return card;
  }));}
  if(!a)return;
  $('account-title').textContent=a.name;$('status').textContent=a.status;
  $('start').disabled=a.desired;$('stop').disabled=!a.desired&&!a.online&&!a.pendingAt;$('password').disabled=a.desired;
  $('session-hint').textContent=a.hasToken?'Вход сохранён. Оставь пароль пустым для входа по токену.':'Пароль не сохраняется. Steam может запросить код из приложения или почты.';
  $('guard-area').hidden=!a.guard;if(a.guard){$('guard-label').textContent=a.guard.wrong?'Дождись нового кода Steam Guard':a.guard.kind==='email'?'Код из письма Steam':'Код Steam Guard';$('guard-send').disabled=Date.now()<a.guard.waitUntil;}
  $('active-time').textContent=duration(a.activeMs);$('game-time').textContent=hours(a.gameMs);
  $('batch-info').textContent=a.batchCount?`${a.batchIndex%a.batchCount+1}/${a.batchCount} · ${duration(a.remainingMs)}`:'—';
  $('queue-summary').textContent=`${a.appids.length} игр · до ${a.batchSize} одновременно · ротация ${a.rotationMinutes} мин`;
  $('current').textContent=a.current.length?'Сейчас: '+a.current.map(id=>gameName(a,id)).join(', '):'Активных игр нет';
  $('next').disabled=!a.online||a.blocked||a.batchCount<2;
  $('automation-state').textContent=[a.pendingAt?'Автовход через '+duration(a.pendingAt-Date.now()):'',a.retryAt?'Повтор подключения через '+duration(a.retryAt-Date.now()):'',a.stopAt?'Остановка через '+duration(a.stopAt-Date.now()):''].filter(Boolean).join(' · ');
  const qk=JSON.stringify([a.id,a.appids,a.batchIndex,a.current,a.library.length]);
  if(qk!==queueKey){queueKey=qk;const index=a.batchCount?a.batchIndex%a.batchCount:0;
    const batches=[...a.nextBatches.slice(index),...a.nextBatches.slice(0,index)];
    $('queue').replaceChildren(...batches.map((batch,n)=>{const box=el('div',undefined,'batch-card'+(n===0?' active':''));box.append(el('strong',`Партия ${batch.index+1} · ${n===0?'текущая по очереди':n===1?'следующая':'далее'}`),el('p',batch.appids.map(id=>gameName(a,id)).join(', '),'muted'));return box;}));
  }
}
function renderPresets(a){
  const key=JSON.stringify([a.id,a.data.presets,state.presets]);if(key===presetKey)return;presetKey=key;
  $('preset-list').replaceChildren(...[...state.presets,...a.data.presets].map(p=>{const card=el('section',undefined,'panel');card.append(el('span',a.data.presets.includes(p)?'СВОЙ НАБОР':'ГОТОВЫЙ НАБОР','eyebrow'),el('h2',p.name),el('p',`${p.appids.length} игр · ${Math.ceil(p.appids.length/a.batchSize)} партий`,'muted'),el('p',p.appids.map(id=>gameName(a,id)).join(', '),'preset-description'));
    const row=el('div',undefined,'row');row.append(btn('Применить',async()=>{if(dirty&&!confirm('Заменить несохранённый выбор игр пресетом?'))return;const r=await command('preset-apply',{id:a.id,preset:p.id});if(r){fill(currentAccount());notice('Пресет применён и сохранён');}}));
    if(a.data.presets.includes(p))row.append(btn('Удалить',()=>{if(confirm('Удалить пресет «'+p.name+'»?'))command('preset-delete',{id:a.id,preset:p.id});}));card.append(row);return card;
  }));
}
function tableRow(values){const row=el('tr');row.append(...values.map(v=>el('td',v)));return row;}
function renderStats(a){
  const today=dateKey(new Date());$('stats-today').textContent=hours(state.accounts.reduce((sum,a)=>sum+(a.data.days[today]?.gameMs||0),0))+' ч';
  $('stats-total').textContent=hours(state.accounts.reduce((sum,a)=>sum+a.data.gameMs,0))+' ч';$('stats-rate').textContent=state.accounts.reduce((sum,a)=>sum+a.current.length,0)+' ч/ч';
  const scope=$('stats-scope').value;const accounts=scope==='all'?state.accounts:state.accounts.filter(a=>a.id===scope),days={};
  $('stats-accounts').replaceChildren(...accounts.map(a=>el('p',`${a.name}: сегодня ${hours(a.data.days[today]?.gameMs||0)} ч · всего ${hours(a.data.gameMs)} ч`,'muted')));
  for(const a of accounts)for(const [date,d]of Object.entries(a.data.days)){const day=days[date]||={activeMs:0,gameMs:0};day.activeMs+=d.activeMs;day.gameMs+=d.gameMs;}
  $('daily-stats').replaceChildren(...Object.entries(days).sort((a,b)=>b[0].localeCompare(a[0])).map(([date,d])=>tableRow([date,hours(d.activeMs)+' ч',hours(d.gameMs)+' ч'])));
  if(!Object.keys(days).length)$('daily-stats').append(tableRow(['Пока нет истории','0 ч','0 ч']));
  if(!a)return;
  $('goal-list').replaceChildren(...a.goals.map(g=>{const row=el('div',undefined,'goal-row');const body=el('div');body.append(el('strong',gameName(a,g.appid)),el('p',`${g.minutes==null?'Нет серверных данных':(g.minutes/60).toFixed(2)+' ч'} / ${g.hours} ч · ${g.basis==='steam'?'Steam':'локально'}${g.notified?' · достигнуто':''}`,'muted'));const progress=el('progress');progress.max=g.hours*60;progress.value=g.minutes||0;progress.setAttribute('aria-label','Прогресс цели '+g.appid);body.append(progress);row.append(body,btn('Удалить',()=>command('goal-delete',{id:a.id,appid:g.appid})));return row;}));
  if(!a.goals.length)$('goal-list').append(el('p','Добавь цель, чтобы видеть прогресс и получить уведомление.','muted'));
  $('server-updated').textContent='Серверные данные: '+stamp(a.data.libraryAt)+'. Steam может обновлять часы с задержкой.';
  const ids=[...new Set([...a.appids,...Object.keys(a.data.games).map(Number),...a.library.map(g=>g.appid)])];
  $('server-stats').replaceChildren(...ids.map(id=>{const g=a.library.find(g=>g.appid===id);return tableRow([gameName(a,id)+' · #'+id,g?((g.playtime_forever||0)/60).toFixed(2)+' ч':'—',g?.playtime_2weeks==null?'—':(g.playtime_2weeks/60).toFixed(2)+' ч',hours(a.data.games[id]||0)+' ч']);}));
}
function renderDiagnostics(){
  const h=state.health;if(!h)return;$('uptime').textContent=duration(h.uptimeMs);$('memory').textContent=h.memoryMB+' МБ';$('heartbeat').textContent=new Date(h.heartbeat).toLocaleTimeString('ru-RU');
  $('health-accounts').replaceChildren(...state.accounts.map(a=>el('p',`${a.name}: ${a.status} · ${a.online?'подключён':'не подключён'} · повторных ошибок: ${a.failures}${a.retryAt?' · повтор '+stamp(a.retryAt):''}`,'muted')));
  $('traffic').textContent=`HTTP за запуск: получено ${(h.bytes.httpReceived/1024).toFixed(1)} КБ, отправлено ${(h.bytes.httpSent/1024).toFixed(1)} КБ · запросов: ${h.bytes.requests}. Режим: ${state.options.trafficMode==='low'?'экономный':'обычный'}; обновление Steam: ${state.options.libraryHours?state.options.libraryHours+' ч':'только вручную'}.`;
  $('diagnostic-current').replaceChildren(...state.accounts.map(a=>el('p',`${a.name}: ${a.current.length} игр${a.current.length?' — '+a.current.map(id=>gameName(a,id)).join(', '):''}`,'muted')));
  $('diagnostic-telegram').textContent=state.telegram.status+(state.telegram.enabled?' · бот включён':' · бот выключен')+(state.telegram.hasToken?' · токен сохранён':' · токен не задан');
  $('logs').replaceChildren(...state.logs.slice().reverse().map(l=>el('div',`${stamp(l.time)}  ${l.account}: ${l.message}`)));
  $('notifications').replaceChildren(...state.notifications.slice().reverse().map(n=>el('p',stamp(n.time)+' · '+n.message)));
}
const optionFields={startupDelay:'startup-delay',libraryHours:'library-hours',trafficMode:'traffic-mode',startMinimized:'start-minimized',scheduleEnabled:'schedule-enabled',scheduleStart:'schedule-start',scheduleEnd:'schedule-end',breakEvery:'break-every',breakMinutes:'break-minutes',stopAfter:'stop-after'};
function initForms(){
  for(const [key,id]of Object.entries(optionFields)){const input=$(id);if(input.type==='checkbox')input.checked=state.options[key];else input.value=state.options[key];}
  $('schedule-days').replaceChildren(...[1,2,3,4,5,6,0].map(day=>{const label=el('label',undefined,'check');const check=el('input');check.type='checkbox';check.value=day;check.checked=state.options.scheduleDays.includes(day);label.append(check,document.createTextNode(['Вс','Пн','Вт','Ср','Чт','Пт','Сб'][day]));return label;}));
  $('telegram-chat').value=state.telegram.chatId;$('telegram-enabled').checked=state.telegram.enabled;$('telegram-errors').checked=state.telegram.errors;$('telegram-daily').checked=state.telegram.daily;$('telegram-time').value=state.telegram.dailyTime;
}
function renderProfiles(){
  const profiles=state.profiles||[];
  $('profile-list').replaceChildren(...profiles.map(p=>{const row=el('div',undefined,'row profile-row');row.append(el('span',`${p.name} · ${p.accounts.length} аккаунта(ов)`,'muted'),btn('Применить',()=>command('profile-apply',{profile:p.id})),btn('Удалить',()=>{if(confirm('Удалить профиль «'+p.name+'»?'))command('profile-delete',{profile:p.id});}));return row;}));
  if(!profiles.length)$('profile-list').append(el('p','Сохрани текущие наборы игр и автоматизацию, чтобы переключать их одной кнопкой.','muted'));
  const u=state.update||{};$('update-status').textContent=u.status||'Проверка обновлений ещё не выполнялась';$('update-link').hidden=!u.available||!u.url;if(u.available)$('update-link').href=u.url;
}
function render(next){
  state=next;if(!state.accounts.some(a=>a.id===selected)){selected=state.accounts[0]?.id||null;if(selected)fill(currentAccount(),true);}
  const a=currentAccount();
  if(a&&!dirty&&JSON.stringify(a.appids)!==JSON.stringify(selectedIds().map(Number)))fill(a);
  $('count').textContent=state.accounts.length+' / 3';$('add-form').hidden=state.accounts.length>=3;
  $('autolaunch').checked=!!state.autoLaunch;
  $('empty').hidden=!!a||!['boost','games','presets'].includes(page);
  document.querySelectorAll('.account-required').forEach(e=>e.hidden=!a);
  document.querySelectorAll('.page').forEach(e=>e.hidden=e.id!=='page-'+page||(e.classList.contains('account-required')&&!a));
  const key=JSON.stringify(state.accounts.map(a=>[a.id,a.name,a.status,a.id===selected]));
  if(key!==navKey){navKey=key;$('accounts').replaceChildren(...state.accounts.map(a=>{const b=btn(a.name,()=>select(a.id),'account-button'+(a.id===selected?' selected':''));b.append(el('span',a.status));return b;}));
    const scope=$('stats-scope').value;$('stats-scope').replaceChildren(el('option','Все аккаунты'));$('stats-scope').firstChild.value='all';for(const a of state.accounts){const o=el('option',a.name);o.value=a.id;$('stats-scope').append(o);}if([...$('stats-scope').options].some(o=>o.value===scope))$('stats-scope').value=scope;
  }
  if(!state.options)return;
  const nextOptionsKey=JSON.stringify(state.options);if(!formsReady||optionsKey!==nextOptionsKey){formsReady=true;optionsKey=nextOptionsKey;initForms();}
  $('network-state').textContent=state.health.network?'● Сеть доступна':'○ Ожидание сети';$('data-path').textContent='Данные: '+state.dataPath;
  $('telegram-status').textContent=state.telegram.status;$('telegram-token-hint').textContent=state.telegram.hasToken?'Токен сохранён. Оставь поле пустым, чтобы сохранить текущий.':'Токен ещё не сохранён.';
  if(page==='boost')renderBoost(a);
  if(page==='games'&&a){$('load-games').disabled=!a.online;$('free-license').disabled=!a.online;renderLibrary(a);}
  if(page==='presets'&&a)renderPresets(a);
  if(page==='stats')renderStats(a);
  if(page==='diagnostics')renderDiagnostics();
  if(page==='settings')renderProfiles();
}
async function save(){const id=selected;const answer=await command('save',{id,appids:$('appids').value,batchSize:Number($('batch-size').value),rotationMinutes:Number($('rotation').value),autoStart:$('auto-start').checked});if(answer&&selected===id){fill(currentAccount());$('dirty').textContent='Сохранено';notice('Настройки игр сохранены');}return answer;}
document.querySelectorAll('[data-page]').forEach(b=>b.onclick=()=>switchPage(b.dataset.page));
$('add-form').onsubmit=async event=>{event.preventDefault();const r=await command('add',{name:$('new-name').value});if(r){$('new-name').value='';select(r.result);switchPage('boost');}};
$('start').onclick=async()=>{const id=selected;if(dirty&&!await save())return;const password=$('password').value;$('password').value='';notice('');await command('start',{id,password});};
$('start-all').onclick=async()=>{if(dirty&&!await save())return;await command('start-all');};
$('stop-all').onclick=()=>command('stop-all');$('stop').onclick=()=>command('stop',{id:selected});$('save').onclick=save;$('next').onclick=()=>command('next',{id:selected});
$('guard-send').onclick=async()=>{const code=$('guard-code').value;$('guard-code').value='';await command('guard',{id:selected,code});};
$('guard-code').onkeydown=e=>{if(e.key==='Enter'&&!$('guard-send').disabled)$('guard-send').click();};$('password').onkeydown=e=>{if(e.key==='Enter'&&!$('start').disabled)$('start').click();};
$('load-games').onclick=async()=>{notice('Загрузка библиотеки Steam…');if(await command('library',{id:selected})){libraryKey='';render(state);notice('Библиотека обновлена');}};
$('search').oninput=()=>renderLibrary(currentAccount());
$('select-all').onclick=()=>{const ids=new Set(selectedIds());visibleGames(currentAccount()).forEach(g=>ids.add(String(g.appid)));$('appids').value=[...ids].join(', ');markDirty();renderLibrary(currentAccount());};
$('first32').onclick=()=>{$('appids').value=visibleGames(currentAccount()).slice(0,32).map(g=>g.appid).join(', ');markDirty();renderLibrary(currentAccount());};
$('clear-selection').onclick=()=>{$('appids').value='';markDirty();renderLibrary(currentAccount());};
$('add-popular').onclick=async()=>{if(await command('popular',{id:selected}))notice('Популярные игры добавлены в локальную библиотеку');};
$('custom-add').onclick=async()=>{const r=await command('custom-add',{id:selected,appid:$('custom-appid').value,name:$('custom-name').value});if(r){$('custom-appid').value='';$('custom-name').value='';notice('Игра добавлена в библиотеку. Отметь её для буста.');}};
$('store-search').onclick=async()=>{const id=selected;notice('Поиск в Steam…');const r=await command('search-games',{query:$('search').value});if(!r||id!==selected)return;notice(r.result.length?'Выбери игру для добавления':'Игры не найдены');$('search-results').replaceChildren(...r.result.map(g=>{const row=el('div',undefined,'search-result');row.append(el('span',g.name+' · #'+g.appid),btn('Добавить',()=>command('custom-add',{id,appid:g.appid,name:g.name})));return row;}));};
$('free-license').onclick=async()=>{if(!confirm('Запросить бесплатные лицензии для выбранных игр?'))return;notice('Запрос лицензий…');const r=await command('free-license',{id:selected,appids:selectedIds()});if(r)notice(`Steam выдал игр: ${r.result.apps.length}, пакетов: ${r.result.packages.length}. Для обновления списка используй «Обновить из Steam».`);};
['appids','batch-size','rotation','auto-start'].forEach(id=>$(id).addEventListener('input',markDirty));
$('preset-save').onclick=async()=>{if(await command('preset-save',{id:selected,name:$('preset-name').value,appids:selectedIds()})){$('preset-name').value='';notice('Пресет сохранён');}};
$('goal-save').onclick=async()=>{if(await command('goal-save',{id:selected,appid:$('goal-appid').value,hours:$('goal-hours').value,basis:$('goal-basis').value}))notice('Цель сохранена');};
$('stats-scope').onchange=()=>renderStats(currentAccount());
$('forget').onclick=()=>{if(confirm('Удалить сохранённую сессию? При следующем входе понадобится пароль.'))command('forget',{id:selected});};
$('remove').onclick=()=>{if(confirm('Удалить аккаунт, его настройки, статистику и сохранённый вход?'))command('remove',{id:selected});};
$('autolaunch').onchange=()=>command('autolaunch',{enabled:$('autolaunch').checked});
$('options-form').onsubmit=async e=>{e.preventDefault();const payload={};for(const [key,id]of Object.entries(optionFields)){const input=$(id);payload[key]=input.type==='checkbox'?input.checked:input.type==='number'?Number(input.value):input.value;}payload.scheduleDays=[...$('schedule-days').querySelectorAll('input:checked')].map(e=>Number(e.value));if(await command('options',payload))notice('Автоматизация сохранена');};
$('profile-save').onclick=async()=>{const name=$('profile-name').value;if(await command('profile-save',{name})){$('profile-name').value='';notice('Профиль сохранён');}};
$('check-update').onclick=async()=>{const r=await command('check-update');if(r?.result)notice(r.result);};
$('telegram-form').onsubmit=async e=>{e.preventDefault();const token=$('telegram-token').value;$('telegram-token').value='';if(await command('telegram',{token,enabled:$('telegram-enabled').checked,chatId:$('telegram-chat').value,errors:$('telegram-errors').checked,daily:$('telegram-daily').checked,dailyTime:$('telegram-time').value}))notice('Настройки Telegram сохранены');};
for(const [id,name]of [['backup-export','export-backup'],['backup-restore','restore-backup']])$(id).onclick=async()=>{const password=$('backup-password').value;$('backup-password').value='';const r=await command(name,{password,includeTokens:$('backup-tokens').checked});if(r?.result)notice(r.result);};
$('export-diagnostics').onclick=async()=>{const r=await command('export-diagnostics');if(r?.result)notice(r.result);};
$('quit').onclick=()=>command('quit');window.steamHours.subscribe(render);window.steamHours.onError(notice);command('state');
