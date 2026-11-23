import http from 'node:http';
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import path from 'node:path';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
const assets=path.resolve('cmd/panel/web'), calls=[];
let connected=false,key=false;
const srv=http.createServer(async(req,res)=>{
  if(req.url!='/rpc'){
    const p=req.url==='/'?'index.html':req.url.slice(1);
    if(!['app.js','index.html','app.css'].includes(p)){res.writeHead(404);return res.end()}
    res.setHeader('Content-Type',{'index.html':'text/html; charset=utf-8','app.js':'text/javascript','app.css':'text/css'}[p]);
    return res.end(await fs.readFile(path.join(assets,p)));
  }
  let body=''; for await(const c of req)body+=c;
  const q=JSON.parse(body);calls.push(q.method);
  if(q.method==='account.setKey')key=true;
  if(q.method==='tunnel.connect')connected=true;
  if(q.method==='tunnel.disconnect')connected=false;
  const result=q.method==='diagnostics.ip'?{time:new Date().toISOString(),wan:{ip:'192.0.2.1'},tunnel:connected?{ip:'203.0.113.7'}:{ip:null,status:'disconnected'}}:
    {core:{core_responding:true,core_state:connected?1:0},has_key:key,servers:key?[{id:4,name:'Test',shortname:'FI'}]:[],key_source:'file'};
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({id:q.id,result}));
});
await new Promise(r=>srv.listen(0,'127.0.0.1',r));
const home=await fs.mkdtemp(path.resolve('cache/chrome-')), addr='http://127.0.0.1:'+srv.address().port;
const chrome=spawn('C:/Program Files/Google/Chrome/Application/chrome.exe',['--headless','--disable-gpu','--disable-extensions','--disable-background-networking','--no-first-run','--remote-debugging-port=0','--user-data-dir='+home,addr],{stdio:'ignore',windowsHide:true});
let sock;
try{
  let port;for(let i=0;i<100&&!port;i++){try{port=(await fs.readFile(path.join(home,'DevToolsActivePort'),'utf8')).split('\n')[0]}catch{await delay(100)}}
  const pages=await (await fetch('http://127.0.0.1:'+port+'/json/list')).json();
  sock=new WebSocket(pages.find(x=>x.url.startsWith(addr)).webSocketDebuggerUrl);await new Promise(r=>sock.onopen=r);
  let seq=0;const pending=new Map();sock.onmessage=e=>{const d=JSON.parse(e.data);if(d.id)pending.get(d.id)?.(d)};
  const send=(method,params={})=>new Promise(r=>{const id=++seq;pending.set(id,r);sock.send(JSON.stringify({id,method,params}))});
  const run=async(expression)=>(await send('Runtime.evaluate',{expression,returnByValue:true})).result.result.value;
  const wait=async e=>{for(let i=0;i<60;i++){if(await run(e))return;await delay(100)}throw Error(e)};
  await wait('document.querySelector("#core")?.textContent==="Ядро доступно"');
  await run('document.querySelector("#account").click();document.querySelector("#key").value="test-value";document.querySelector("#login").click()');
  await wait('!document.querySelector("dialog").open && !document.querySelector("#connect").disabled');
  await run('document.querySelector("#connect").click()');
  await wait('document.querySelector("#connection").textContent==="Туннель запущен"');
  await run('document.querySelector("#checkip").click()');
  await wait('document.querySelector("#tunnel").textContent==="203.0.113.7"');
  for(const width of [1200,375]){
    await send('Emulation.setDeviceMetricsOverride',{width,height:800,deviceScaleFactor:1,mobile:false});
    assert(await run('document.documentElement.scrollWidth<=innerWidth'));
    const pic=await send('Page.captureScreenshot',{format:'png'});await fs.writeFile('cache/panel-preview-'+width+'.png',Buffer.from(pic.result.data,'base64'));
  }
  await run('document.querySelector("#disconnect").click()');await wait('document.querySelector("#connection").textContent==="Туннель выключен"');
  assert(calls.includes('diagnostics.ip') && calls.includes('account.setKey'));console.log('Browser actions and desktop/mobile layout passed with isolated fixture.');
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill();srv.close()}
