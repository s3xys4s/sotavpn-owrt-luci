import http from 'node:http';
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import path from 'node:path';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
const home=await fs.mkdtemp(path.resolve('cache/luci-check-'));
const addr='http://192.168.1.88:8081/cgi-bin/luci/admin/services/sota/status';
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

  const until=async (expr,ms=18000)=>{for(let i=0;i<ms/200;i++){if(await run(expr))return;await delay(200)}throw Error(expr+' '+await run('document.body.innerText.slice(0,1400)'))};
  await until('document.querySelector("input[name=luci_username]")');
  await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_username]").form.submit()');
  await until('document.body.innerText.includes("Ключ сохранён")');
  await send('Page.navigate',{url:addr.replace('/status','/network')});
  await until('document.querySelector("#sota-rules") && document.querySelector("#sota-enabled").checked');
  assert(await run('document.body.innerText.includes("Устройства, для которых действуют правила") && !document.body.innerText.includes("Защищать сеть")'));
  const urls=["https://iplist.opencck.org/?format=text&data=cidr4&site=daramalive.life&site=doramy.club&site=filmix.fm&site=hdrezka.ag&site=kino.watch&site=kinobase.org&site=kinovod.pro&site=kinozal.tv&site=lostfilm.tv&site=netflix.com&site=seasonvar.ru&site=stremio.com&site=themoviedb.org&site=zeflix.online","https://iplist.opencck.org/?format=text&data=domains&wildcard=1&site=daramalive.life&site=doramy.club&site=filmix.fm&site=hdrezka.ag&site=kino.watch&site=kinobase.org&site=kinovod.pro&site=kinozal.tv&site=lostfilm.tv&site=netflix.com&site=seasonvar.ru&site=stremio.com&site=themoviedb.org&site=zeflix.online"], manual="1.1.1.1/32\n192.168.1.1/24\ngoogle.com\n*.microsoft.com\nsota-both.example\n*.sota-both.example";
  const rows='.sota-source-row input';
  if (process.argv[2]==='remove') {
    await send('Page.navigate',{url:'http://192.168.1.88:8080/'});
    await until('document.querySelectorAll(".sota-source-row input").length===2');
    assert(await run('document.querySelector("#sota-rules").value==='+JSON.stringify(manual)));
    assert(await run('document.querySelector(".sota-source-row input").value==='+JSON.stringify(urls[0])));
    await run('document.querySelector(".sota-source-row button").click();document.querySelector(".sota-routing").requestSubmit()');
    await until('document.querySelector(".sota-routing [role=status]").textContent==="Правила применены"',35000);
    await send('Page.navigate',{url:addr.replace('/status','/network')});
    await until('document.querySelectorAll(".sota-source-row input").length===1 && document.querySelector(".sota-source-row input").value==='+JSON.stringify(urls[1]));
    assert(await run('document.querySelector("#sota-rules").value==='+JSON.stringify(manual)));
    console.log("Minus removed only the selected source. Other source and manual rules survived across both panels.");
  } else {
    assert(await run('document.querySelector("#sota-rules").value==="" && document.querySelectorAll(".sota-source-row input").length===0'));
    await run('document.querySelector("#sota-rules").value='+JSON.stringify(manual));
    for(let i=0;i<urls.length;i++){
      await run('document.querySelector(".sota-sources>button").click()');
      await run('document.querySelectorAll(".sota-source-row input")['+i+'].value='+JSON.stringify(urls[i]));
    }
    await run('document.querySelector(".sota-routing").requestSubmit()');
    await until('document.querySelector(".sota-routing [role=status]").textContent==="Правила применены"',35000);
    await send('Page.navigate',{url:'http://192.168.1.88:8080/'});
    await until('document.querySelectorAll(".sota-source-row input").length===2');
    assert.deepEqual(await run('Array.from(document.querySelectorAll(".sota-source-row input"),i=>i.value)'),urls);
    assert(await run('document.querySelector("#sota-rules").value==='+JSON.stringify(manual)));
    console.log("Plus added both supplied HTTP sources. Settings and manual rules matched after navigating to main panel.");
  }
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill()}