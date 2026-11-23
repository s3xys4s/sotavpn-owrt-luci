import http from 'node:http';
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import path from 'node:path';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
const home=await fs.mkdtemp(path.resolve('cache/luci-check-'));
const [mode] = process.argv.slice(2);
const addr=mode==='setup'?'http://192.168.1.88:8080/':'http://192.168.1.88:8081/cgi-bin/luci/admin/services/sota/network';
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

  const until=async (expr,ms=18000)=>{for(let i=0;i<ms/200;i++){if(await run(expr))return;await delay(200)}throw Error(expr+' | '+await run('document.body.innerText.slice(0,550)'))};
  const nav=async url=>{await send('Page.navigate',{url});};
  const check=async expr=>assert.ok(await run(expr),expr);


  await until('document.querySelector("input[name=luci_username]")');
  await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_username]").form.submit()');
  await until('document.querySelector("#sota-device")?.value');
  await check('!document.querySelector("#sota-rules") && !document.querySelector("#sota-exception-rules")');
  const go=async id=>{await nav(addr.replace(/network$/,id));await until('document.querySelector(".sota-routing .primary:not(:disabled)")');};
  const apply=async()=>{await run('document.querySelector(".sota-routing").requestSubmit()');await until('["Rules applied","Правила применены"].includes(document.querySelector(".sota-routing [role=status]")?.textContent)',55000)};
  await go("lists");
  await check('!!document.querySelector("#sota-rules") && !document.querySelector("#sota-device") && !document.querySelector("#sota-exception-rules")');
  const sel='.sota-sources .sota-source-row input[type=url]';
  const urls=await run('Array.from(document.querySelectorAll('+JSON.stringify(sel)+'),n=>n.value)');
  assert.equal(urls.filter(u=>u.startsWith("https://iplist.opencck.org/?format=text&")).length,10);
  await run('document.querySelector(".sota-sources>button").click(); const x=Array.from(document.querySelectorAll(".sota-source-row input")).at(-1);x.value="https://example.org/not-built-in.txt";x.closest(".sota-source-row").querySelector("button").click()');
  await check('!document.querySelector(".sota-restore button")');
  const url=urls.find(x=>x.includes("data=cidr4") && x.includes("site=x.com"));
  await run('Array.from(document.querySelectorAll(".sota-source-row input")).find(x=>x.value==='+JSON.stringify(url)+').closest(".sota-source-row").querySelector("button").click()');
  await apply();await go("lists");
  await check('document.querySelectorAll(".sota-restore button").length===1 && document.querySelector(".sota-restore button").textContent.includes("Twitter")');
  await run('document.querySelector(".sota-restore button").click()');await apply();
  await go("exceptions");
  await check('!!document.querySelector("#sota-exception-rules") && !document.querySelector("#sota-rules")');
  await until('Array.from(document.querySelectorAll(".sota-file-row span")).some(x=>x.textContent.includes("Sota Connect"))');
  await run('Array.from(document.querySelectorAll(".sota-file-row")).find(x=>x.textContent.includes("Sota Connect")).querySelector("button").click()');await apply();
  await go("exceptions");await check('document.querySelectorAll(".sota-restore button").length===1');
  await run('document.querySelector(".sota-restore button").click()');await apply();
  await nav('http://192.168.1.88:8080/');
  await until('document.querySelector("#sota-exception-rules") && document.querySelector(".sota-file-row span")');
  await run('document.querySelector("[data-language=en]").click()');
  await check('document.body.innerText.includes("Routing lists") && document.body.innerText.includes("Exceptions") && !document.querySelector(".sota-restore button")');
  console.log("LuCI pages, 10 editable text sources, removal across reload, restoring built-ins only, and standalone English panel passed.");
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill()}

