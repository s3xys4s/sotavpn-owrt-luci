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
  await until('document.querySelector("#sota-rules") && document.querySelector("#sota-enabled").checked && document.querySelector("#sota-dns").value==="1.1.1.1"');
  await run(`(()=>{const f=document.querySelector('#sota-import'),dt=new DataTransfer();dt.items.add(new File(['# ui-roundtrip\\nDOMAIN-SUFFIX,example.org'],'list.txt',{type:'text/plain'}));f.files=dt.files;f.dispatchEvent(new Event('change'));})()`);
  await until('document.querySelector("#sota-rules").value.includes("ui-roundtrip")');
  await run('document.querySelector(".sota-routing").requestSubmit()');
  await until('document.body.innerText.includes("Правила применены")');
  await send('Page.navigate',{url:'http://192.168.1.88:8080/'});
  await until('document.querySelector("#sota-rules")?.value.includes("ui-roundtrip") && document.querySelector("#runtime")?.value==="openwrt"');
  await until('document.querySelector("#connection")?.textContent==="Туннель запущен"');
  await run('document.querySelector("#checkip").click()');
  await until(String.raw`/^(\d+\.){3}\d+$/.test(document.querySelector("#tunnel").innerText) && document.querySelector("#wan").innerText!==document.querySelector("#tunnel").innerText`);
  await until('document.querySelector("#events").innerText.includes("Применение правил")');
  await run('document.querySelector("#sota-rules").value+="\\n# via-main";document.querySelector(".sota-routing").requestSubmit()');
  await until('document.body.innerText.includes("Правила применены")');
  await send('Page.navigate',{url:addr.replace('/status','/network')});
  await until('document.querySelector("#sota-rules")?.value.includes("via-main")');
  await send('Page.navigate',{url:'http://192.168.1.88:8080/?runtime=linux'});
  await until('document.querySelector("#runtime")?.value==="linux" && document.querySelector("#core")?.textContent==="Ядро доступно"');
  console.log('PASS LuCI LAN login and file import, shared settings both ways, primary panel IP and events, legacy Linux backend');
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill()}