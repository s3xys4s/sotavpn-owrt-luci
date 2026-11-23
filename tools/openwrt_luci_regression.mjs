/* Run with Node 22+ and Chrome. The test uses the existing lab account and sends no key. */
import assert from "node:assert/strict";
import {spawn} from "node:child_process";
import {mkdtemp, readFile, rm} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import {setTimeout as delay} from "node:timers/promises";

const base = new URL(process.argv[2] || "http://192.168.1.88:8081/cgi-bin/luci/admin/services/sota/");
const executable = process.env.CHROME || (process.platform === "win32" ?
  "C:/Program Files/Google/Chrome/Application/chrome.exe" : "chromium");
const dir = await mkdtemp(path.join(tmpdir(),"sota-luci-regression-"));
const chrome = spawn(executable,["--headless","--disable-extensions","--no-first-run",
  "--remote-debugging-port=0","--user-data-dir="+dir,"about:blank"],{stdio:"ignore",windowsHide:true});
const stopped = new Promise(resolve=>chrome.once("exit",resolve));
let socket;
try {
  let port;
  for(let i=0;i<100&&!port;i++){
    try {port=(await readFile(path.join(dir,"DevToolsActivePort"),"utf8")).split("\n")[0]} catch {await delay(100)}
  }
  const pages=await (await fetch("http://127.0.0.1:"+port+"/json/list")).json();
  socket=new WebSocket(pages.find(x=>x.type==="page").webSocketDebuggerUrl);
  await new Promise((ok,fail)=>{socket.onopen=ok;socket.onerror=fail});
  let seq=0;
  const pending=new Map();
  socket.onmessage=e=>{const v=JSON.parse(e.data);if(v.id){pending.get(v.id)?.(v);pending.delete(v.id)}};
  function send(method,params={}){
    return new Promise(resolve=>{const id=++seq;pending.set(id,resolve);socket.send(JSON.stringify({id,method,params}))});
  }
  async function run(expression){
    const response=await send("Runtime.evaluate",{expression,returnByValue:true});
    if(response.result?.exceptionDetails)throw Error(response.result.exceptionDetails.text);
    return response.result?.result.value;
  }
  async function until(expression){
    for(let i=0;i<120;i++){if(await run(expression))return;await delay(200)}
    throw Error(expression);
  }
  const nav=page=>send("Page.navigate",{url:new URL(page,base).href});
  const button=text=>"Array.from(document.querySelectorAll('button')).find(b=>b.textContent==="+JSON.stringify(text)+")";
  await send("Network.enable");
  for(const lang of ["ru","en"]) {
    const en=lang==="en";
    await send("Network.setExtraHTTPHeaders",{headers:{"Accept-Language":en?"en-US,en;q=0.9":"ru-RU,ru;q=0.9"}});
    await nav("status");
    await until('document.querySelector("input[name=luci_username]") || document.querySelector("#sota-hwid")');
    if(await run('!!document.querySelector("input[name=luci_username]")')){
      await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_password]").value='+JSON.stringify(process.env.LUCI_PASSWORD || "")+';document.querySelector("input[name=luci_username]").form.submit()');
    }
    await until('document.documentElement.lang.startsWith('+JSON.stringify(lang)+') && document.querySelector("#sota-hwid")?.disabled && /^[0-9a-f]{64}$/.test(document.querySelector("#sota-hwid").value)');
    const probe=en?"Check IP":"Проверить IP";
    await until(button(probe)+' && !'+button(probe)+'.disabled');
    await run(button(probe)+'.click()');
    await until('/WAN: (\\d+\\.){3}\\d+/.test(document.body.innerText)');
    const addresses=await run('document.body.innerText.match(/WAN: ([\\d.]+) · [^:]+: ([\\d.]+)/)?.slice(1)');
    assert.equal(addresses?.length,2);assert.notEqual(addresses[0],addresses[1]);
    await until('document.querySelector("[aria-live=off] strong")');
    await run('(()=>{const f=Array.from(document.querySelectorAll("select")).find(s=>Array.from(s.options).some(o=>o.value==="warn"));f.value="warn";f.dispatchEvent(new Event("change"));document.querySelector("input[type=checkbox]").click();})()');
    assert.ok(await run('document.querySelector("input[type=checkbox]").checked'));
    await nav("network");await until('document.querySelector("#sota-enabled") && document.querySelector("#sota-device").value');
    await nav("lists");await until('document.querySelector("#sota-rules") && document.querySelector("input[type=url]")');
    assert.ok(await run('document.querySelector("label[for=sota-rules]").textContent==='+JSON.stringify(en?"Manual rules":"Ручные правила")));
    await nav("exceptions");await until('document.querySelector("#sota-exception-rules") && document.querySelector(".sota-file-row")');
    console.log("PASS LuCI "+lang+" – account HWID, IP, log controls, network, routing lists and exceptions");
  }
  // Use the saved selection on a routing page as well as the status page.
  await send("Emulation.setDeviceMetricsOverride",{width:375,height:800,deviceScaleFactor:1,mobile:true});
  const geometry=await run('Array.from(document.querySelectorAll(".sota-service-actions button"),b=>[b.textContent,b.getBoundingClientRect().top,getComputedStyle(b).backgroundColor])');
  assert.deepEqual(geometry.map(x=>x[0]),["Start","Stop","Restart"]);
  assert.equal(new Set(geometry.map(x=>x[1])).size,1);
  assert.equal(new Set(geometry.map(x=>x[2])).size,3);
  await run('document.querySelector(".sota-stop").click()');
  await until('!document.querySelector(".sota-start").disabled && document.querySelector(".sota-stop").disabled');
  await run('document.querySelector(".sota-start").click()');
  await until('document.querySelector(".sota-start").disabled && !document.querySelector(".sota-stop").disabled');
  await run('document.querySelector(".sota-restart").click()');
  await until('document.querySelector(".sota-start").disabled && !document.querySelector(".sota-stop").disabled');
  console.log("PASS mobile service toolbar, saved start, stop and restart");
  // Exercise the native LuCI transport, including a multi-chunk file.
  await nav("lists");
  await until('document.querySelector(".sota-routing .primary:not(:disabled)")');
  const rules='document.querySelector("#sota-rules")';
  const original=await run(rules+'.value');
  const file="sota-luci-"+Date.now()+".txt", line="*."+file+".invalid";
  const edited=[original,line].filter(Boolean).join("\n");
  const fileRow='Array.from(document.querySelectorAll(".sota-file-row")).find(x=>x.querySelector("span")?.textContent==='+JSON.stringify(file)+')';
  async function apply(){
    await run('document.querySelector(".sota-routing").requestSubmit()');
    await until('document.querySelector(".sota-routing [role=status]")?.textContent==="Rules applied" && !document.querySelector(".sota-routing .primary").disabled');
  }
  try {
    await run(rules+'.value='+JSON.stringify(edited));
    await run('(()=>{const dt=new DataTransfer();dt.items.add(new File(["#"+"x".repeat(19000)+"\\n198.51.100.212/32"],'+JSON.stringify(file)+'));const p=document.querySelector(".sota-files input");p.files=dt.files;p.dispatchEvent(new Event("change"));})()');
    await until(fileRow);
    await apply();
    await nav("lists");await until(fileRow);
    assert.equal(await run(rules+".value"),edited);
  } finally {
    await nav("lists");
    await until('document.querySelector(".sota-routing .primary:not(:disabled)")');
    const stored=await run(rules+".value");
    if(stored.split("\n").includes(line) || await run('Boolean('+fileRow+')')){
      await run(rules+'.value='+JSON.stringify(stored===edited?original:stored.split("\n").filter(x=>x!==line).join("\n")));
      await run(fileRow+'?.querySelector("button").click()');
      await apply();
    }
  }
  await nav("lists");await until('document.querySelector(".sota-routing .primary:not(:disabled)")');
  assert.ok(!await run('Boolean('+fileRow+')') && !(await run(rules+".value")).split("\n").includes(line));
  console.log("PASS native LuCI apply, persisted wildcard, multi-chunk file and removal");
} finally {
  if(socket?.readyState===WebSocket.OPEN){socket.send(JSON.stringify({id:999999,method:"Browser.close"}));socket.close()}
  if(chrome.exitCode===null)chrome.kill();
  await stopped;
  await rm(dir,{recursive:true,force:true,maxRetries:4,retryDelay:150});
}

