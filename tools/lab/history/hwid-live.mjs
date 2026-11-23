import {createHash} from 'node:crypto';
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import path from 'node:path';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
const home=await fs.mkdtemp(path.resolve('cache/luci-check-'));
const [mode] = process.argv.slice(2);
const addr=mode==='setup'?'http://192.168.1.88:8080/':'http://192.168.1.88:8081/cgi-bin/luci/admin/services/sota/status';
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



  const [routerDigest,linuxDigest]=process.argv.slice(3);
  async function assertField(selector, path, expected) {
    await until('(()=>{const n=document.querySelector('+JSON.stringify(selector)+');return n?.disabled && n.type==="text" && /^[0-9a-f]{64}$/.test(n.value)})()');
    const displayed=await run('document.querySelector('+JSON.stringify(selector)+').value');
    const response=await fetch("http://192.168.1.88:8080"+path,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({jsonrpc:"2.0",id:1,method:"status",params:{}})});
    const actual=(await response.json()).result.hwid;
    assert.ok(actual===displayed,"field and active backend differ");
    assert.ok(createHash("sha256").update(actual).digest("hex")===expected,"registration changed");
  }
  await until('document.querySelector("input[name=luci_username]")');
  await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_username]").form.submit()');
  await assertField("#sota-hwid","/router-rpc",routerDigest);
  await nav("http://192.168.1.88:8080/");
  await assertField("#hwid","/router-rpc",routerDigest);
  await run('document.querySelector("[data-language=en]").click()');
  await check('document.querySelector("label[for=hwid]").textContent==="Device HWID"');
  await nav("http://192.168.1.88:8080/?runtime=linux");
  await assertField("#hwid","/rpc",linuxDigest);
  await run('document.querySelector("[data-language=ru]").click()');
  await check('document.querySelector("label[for=hwid]").textContent==="HWID устройства"');
  console.log("LuCI, standalone OpenWrt and standalone Linux fields match the active backend and the pre-upgrade device. Disabled input and both locales verified.");
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill()}

