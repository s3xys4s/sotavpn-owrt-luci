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

  const good='http://10.0.2.2:29831/good', offline='http://10.0.2.2:29831/offline';
  const fileA='sota-check-A.txt',fileB='sota-check-B.txt';
  if(mode==='setup'){
    await until('document.querySelector("#sota-refresh_minutes")?.value');
    await check('document.querySelector("#sota-refresh_minutes").value>0');
    const original=await run('document.querySelector("#sota-rules").value');
    assert(!(await run('Array.from(document.querySelectorAll(".sota-source-row input")).some(x=>x.value.includes(":29831/"))')));
    await run('document.querySelector("[data-language=en]").click()');
    await check('document.querySelector(".sota-files legend").textContent==="Lists from files"');
    for(const url of [good,offline]){
      await run('document.querySelector(".sota-sources>button").click()');
      await run('Array.from(document.querySelectorAll(".sota-source-row input")).at(-1).value='+JSON.stringify(url));
    }
    const rows=await run('document.querySelectorAll(".sota-file-row").length');
    const tempFiles=[[fileA,"#"+'x'.repeat(18000)+"\n198.51.100.154/32\nshared.sota-check.example\none.sota-check.example"],[fileB,"*.shared.sota-check.example\n198.51.100.154/32"]];
    await run('(()=>{const p=document.querySelector(".sota-files input[type=file]"), dt=new DataTransfer(); for(const [name,text] of '+JSON.stringify(tempFiles)+')dt.items.add(new File([text],name));p.files=dt.files;p.dispatchEvent(new Event("change"));})()');
    await until('document.querySelectorAll(".sota-file-row").length==='+String(rows+2));
    assert.equal(await run('document.querySelector("#sota-rules").value'),original);
    await run('document.querySelector("#sota-refresh_minutes").value="1";document.querySelector(".sota-routing").requestSubmit()');
    await until('document.querySelector(".sota-routing [role=status]").textContent==="Rules applied"',42000);
    assert.equal(await run('document.querySelector("#sota-rules").value'),original);
    await check('document.querySelectorAll(".sota-file-row button")[0].title==="Remove file"');
    console.log("Standalone saved two files (chunked upload) plus two URLs and a one-minute schedule; manual rules retained");
  } else {
    await until('document.querySelector("input[name=luci_username]")');
    await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_username]").form.submit()');
    await until('document.querySelector("#sota-rules")');
    await check('document.querySelector("#sota-refresh_minutes").value==="1"');
    for(const name of [fileA,fileB])await check('Array.from(document.querySelectorAll(".sota-file-row span")).some(e=>e.textContent==='+JSON.stringify(name)+')');
    await run('(()=>{const p=document.querySelector(".sota-files input[type=file]"),dt=new DataTransfer();dt.items.add(new File(["#"+ "x".repeat(20000)+"\\n198.51.100.155/32"],"sota-check-C.txt"));p.files=dt.files;p.dispatchEvent(new Event("change"));})()');
    await until('Array.from(document.querySelectorAll(".sota-file-row span")).some(e=>e.textContent==="sota-check-C.txt")');
    await run('Array.from(document.querySelectorAll(".sota-file-row")).find(e=>e.querySelector("span").textContent==='+JSON.stringify(fileA)+').querySelector("button").click();document.querySelector(".sota-routing").requestSubmit()');
    await until('["Rules applied","Правила применены"].includes(document.querySelector(".sota-routing [role=status]").textContent)',42000);
    await nav('http://192.168.1.88:8080/');
    await until('document.querySelector("#sota-rules")');
    await check('!Array.from(document.querySelectorAll(".sota-file-row span")).some(e=>e.textContent==='+JSON.stringify(fileA)+') && Array.from(document.querySelectorAll(".sota-file-row span")).some(e=>e.textContent==='+JSON.stringify(fileB)+')');
    await check('Array.from(document.querySelectorAll(".sota-file-row span")).some(e=>e.textContent==="sota-check-C.txt")');
    console.log("LuCI read both files after reopening, added a new file and removed one. Standalone confirmed all three changes.");
  }
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill()}

