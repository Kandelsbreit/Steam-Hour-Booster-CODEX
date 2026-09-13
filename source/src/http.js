'use strict';
async function jsonRequest(url,body,meter,timeout=20000,signal) {
  const data=body===undefined?undefined:JSON.stringify(body);
  if(meter) { meter.requests++; meter.httpSent+=Buffer.byteLength(data||''); }
  const response=await fetch(url,{method:data?'POST':'GET',body:data,headers:data?{'Content-Type':'application/json'}:{},
    signal:signal?AbortSignal.any([signal,AbortSignal.timeout(timeout)]):AbortSignal.timeout(timeout),redirect:'error'});
  const reader=response.body.getReader(); const chunks=[]; let size=0;
  try {
    for(;;) { const {done,value}=await reader.read(); if(done) break; size+=value.length; if(meter) meter.httpReceived+=value.length;
      if(size>2*1024*1024) throw Error('Слишком большой ответ сервера'); chunks.push(value); }
  } finally { await reader.cancel(); }
  if(!response.ok) throw Error('Сервис недоступен (HTTP '+response.status+')');
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}
async function searchGames(query,meter) {
  query=String(query||'').trim(); if(query.length<2||query.length>100) throw Error('Для поиска введи от 2 до 100 символов');
  const result=await jsonRequest('https://store.steampowered.com/api/storesearch/?l=russian&cc=US&term='+encodeURIComponent(query),undefined,meter);
  return (result.items||[]).filter(g=>Number.isInteger(g.id)&&g.id>0&&g.type==='app').slice(0,40).map(g=>({appid:g.id,name:String(g.name).slice(0,200)}));
}
module.exports={jsonRequest,searchGames};
