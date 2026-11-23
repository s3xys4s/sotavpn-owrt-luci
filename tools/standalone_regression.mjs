import fs from "node:fs/promises";
import path from "node:path";
import {tmpdir} from "node:os";
import {spawn} from "node:child_process";
import {setTimeout as delay} from "node:timers/promises";
import assert from "node:assert/strict";
if (!process.argv[2] || !process.argv[3]) throw Error("Usage: node tools/standalone_regression.mjs PANEL_URL LUCI_SOTA_URL");
const directoryURL=value=>new URL(value.replace(/\/?$/, "/")).href;
const root=directoryURL(process.argv[2]), luci=directoryURL(process.argv[3]);
async function rpc(method,params={}) {
  const response=await fetch(root+"router-rpc",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({jsonrpc:"2.0",id:1,method,params})});
  const {result,error}=await response.json();
  if (error) throw Error(error.message);
  return result;
}
const before=await rpc("network.get"), read=()=>rpc("network.get");
const routeLine="*.sota-web-check.invalid", exceptionLine="sota-web-check.invalid";
const more=(value,line)=>[value,line].filter(Boolean).join("\n");
const newRoutes=more(before.rules,routeLine), newExceptions=more(before.exceptions.rules,exceptionLine);
const file="sota-web-parity-test.txt", url=new URL(before.catalog.at(-1).url);
url.searchParams.sort();const extra=url.href, interval=before.refresh_minutes===359?358:359;
assert.ok(!before.sources.includes(extra)&&!before.rules.includes(routeLine)&&!before.exceptions.rules.includes(exceptionLine)&&!before.files.some(f=>f.name===file));
const home=await fs.mkdtemp(path.join(tmpdir(),"sota-standalone-browser-"));
const chrome=spawn(process.env.CHROME || (process.platform==="win32"?"C:/Program Files/Google/Chrome/Application/chrome.exe":"chromium"),["--headless","--disable-gpu","--disable-extensions","--disable-background-networking","--no-first-run","--remote-debugging-port=0","--user-data-dir="+home,root],{stdio:"ignore",windowsHide:true});
const stopped=new Promise(resolve=>chrome.once("exit",resolve));
let sock;
try {
  let port;for(let i=0;i<100&&!port;i++){try{port=(await fs.readFile(path.join(home,"DevToolsActivePort"),"utf8")).split("\n")[0]}catch{await delay(100)}}
  const pages=await (await fetch("http://127.0.0.1:"+port+"/json/list")).json();
  sock=new WebSocket(pages.find(x=>x.url.startsWith(root)).webSocketDebuggerUrl);await new Promise(r=>sock.onopen=r);
  let seq=0;const pending=new Map();sock.onmessage=e=>{const d=JSON.parse(e.data);if(d.id){pending.get(d.id)?.(d);pending.delete(d.id)}};
  const send=(method,params={})=>new Promise(r=>{const id=++seq;pending.set(id,r);sock.send(JSON.stringify({id,method,params}))});
  async function run(expression){const d=await send("Runtime.evaluate",{expression,returnByValue:true});if(d.result?.exceptionDetails)throw Error(d.result.exceptionDetails.exception.description);return d.result?.result.value;}
  const wait=async(expr,ms=15000)=>{for(let i=0;i<ms/200;i++){if(await run(expr))return;await delay(200)}throw Error(expr+" | "+await run('document.querySelector("#network-editor>div:not([hidden]) [role=status]")?.textContent || ""'))};
  const nav=url=>send("Page.navigate",{url});
  const form='document.querySelector("#network-editor>div:not([hidden]) form")';
  const q=sel=>form+".querySelector("+JSON.stringify(sel)+")";
  const ready=()=>wait(form+'?.querySelector(".primary:not(:disabled)")');
  const go=async page=>{await run('document.querySelector("[data-page='+page+']").click()');await wait('location.hash==="#'+page+'"');await ready()};
  const apply=async()=>{await run(form+'.requestSubmit()');await wait(q('[role=status]')+'.textContent==="Rules applied" && !'+q('.primary')+'.disabled',65000)};
  const set=(sel,v)=>run(q(sel)+'.value='+JSON.stringify(v));
  await wait('document.querySelector("[data-page=status]")?.getAttribute("aria-current")==="page"');
  await run('document.querySelector("[data-language=en]").click()');
  await go("network");
  assert.ok(await run('!!document.querySelector("#sota-doh_url")'));
  await send("Emulation.setDeviceMetricsOverride",{width:375,height:800,deviceScaleFactor:1,mobile:true});
  const geometry=await run('Array.from(document.querySelectorAll(".sota-service-actions button"),b=>[b.textContent,b.getBoundingClientRect().top,getComputedStyle(b).backgroundColor])');
  assert.deepEqual(geometry.map(x=>x[0]),["Start","Stop","Restart"]);
  assert.equal(new Set(geometry.map(x=>x[1])).size,1);
  assert.equal(new Set(geometry.map(x=>x[2])).size,3);
  await wait('!document.getElementById("disconnect").disabled');
  await run('document.getElementById("disconnect").click()');
  await wait('!document.getElementById("connect").disabled && document.getElementById("disconnect").disabled',45000);
  assert.equal((await rpc("status")).want_tunnel,false);
  await run('document.getElementById("connect").click()');
  await wait('!document.getElementById("disconnect").disabled && document.getElementById("connect").disabled',45000);
  await run('document.getElementById("restart").click()');
  await wait('!document.getElementById("restart").disabled && !document.getElementById("disconnect").disabled',45000);
  assert.equal((await rpc("status")).core.core_state,2);
  console.log("PASS standalone mobile toolbar, start, stop, restart and DoH field");
  await go("lists");
  assert.equal(await run('document.querySelector("#routing-title").textContent'),"Routing lists");
  assert.deepEqual(await run('Array.from('+form+'.querySelectorAll("input[type=url]"),x=>x.value)'),before.sources);
  await set("#sota-rules",newRoutes);await set("#sota-refresh_minutes",String(interval));
  await run(q(".sota-sources>button")+".click()");await run(form+'.querySelectorAll("input[type=url]")[ '+before.sources.length+' ].value='+JSON.stringify(extra));
  await run('(()=>{const dt=new DataTransfer();dt.items.add(new File(["# web test\\n198.51.100.211/32\\n*.file-check.invalid"],'+JSON.stringify(file)+'));const p='+q("input[type=file]")+';p.files=dt.files;p.dispatchEvent(new Event("change"));})()');
  await wait(form+'.innerText.includes('+JSON.stringify(file)+')');
  const lastBuiltin=before.catalog.at(-1);
  await run('Array.from('+form+'.querySelectorAll("input[type=url]")).find(x=>x.value==='+JSON.stringify(lastBuiltin.url)+').closest(".sota-source-row").querySelector("button").click()');
  assert.ok(await run(q(".sota-restore button")+'.textContent.includes("Twitter")'));
  await run(q(".sota-restore button")+".click()");
  // Keep the original builtin order, with the test source last.
  await run('(()=>{const inputs=Array.from('+form+'.querySelectorAll("input[type=url]"));const row=inputs.find(x=>x.value==='+JSON.stringify(extra)+').closest(".sota-source-row");row.parentNode.append(row)})()');
  await go("exceptions");
  assert.equal(await run('document.querySelector("#routing-title").textContent'),"Exceptions");
  const russian=before.russian_default;
  await run('Array.from('+form+'.querySelectorAll(".sota-file-row")).find(x=>x.textContent.includes('+JSON.stringify(russian.name)+')).querySelector("button").click()');
  assert.ok(await run(q(".sota-restore button")+'.textContent.includes("Russian")'));
  await run(q(".sota-restore button")+".click()");
  await set("#sota-exception-rules",more([before.bypass,before.exceptions.rules].filter(Boolean).join("\n"),exceptionLine));await apply();
  await go("lists");
  assert.equal(await run(q("#sota-rules")+".value"),newRoutes);
  await run('document.querySelector("[data-language=ru]").click()');assert.equal(await run('document.querySelector("#routing-title").textContent'),"Списки маршрутизации");
  await run('document.querySelector("[data-language=en]").click()');
  await apply();
  const applied=await read();
  assert.equal(applied.rules,newRoutes);assert.ok(applied.exceptions.rules.includes(exceptionLine));assert.equal(applied.refresh_minutes,interval);
  assert.deepEqual(applied.sources,[...before.sources,extra]);assert.ok(applied.files.some(x=>x.name===file));assert.deepEqual(applied.exceptions.files,before.exceptions.files);
  console.log("Standalone saved the URL, TXT, wildcard, update interval and independent exception draft.");
  await nav(luci+"lists");await wait('document.querySelector("input[name=luci_username]") || document.querySelector("#sota-rules")');
  if(await run('!!document.querySelector("input[name=luci_username]")'))await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_password]").value='+JSON.stringify(process.env.LUCI_PASSWORD || "")+';document.querySelector("input[name=luci_username]").form.submit()');
  await wait('document.querySelector("#sota-rules")?.value.includes("sota-web-check")');
  assert.ok(await run('Array.from(document.querySelectorAll(".sota-file-row span")).some(x=>x.textContent==='+JSON.stringify(file)+')'));
  await nav(luci+"exceptions");await wait('document.querySelector("#sota-exception-rules")?.value.includes("sota-web-check")');
  console.log("Both LuCI pages read the changes.");
  await nav(root+"?runtime=linux");await wait('document.querySelector("#runtime")?.value==="linux" && !document.querySelector("#page-nav").hidden');await go("lists");
  assert.equal(await run('document.querySelector("#runtime").value'),"openwrt");
  await run('Array.from('+form+'.querySelectorAll("input[type=url]")).find(x=>x.value==='+JSON.stringify(extra)+').closest(".sota-source-row").querySelector("button").click()');
  await run('Array.from('+form+'.querySelectorAll(".sota-file-row")).find(x=>x.textContent.includes('+JSON.stringify(file)+')).querySelector("button").click()');
  assert.equal(await run(form+'.querySelectorAll(".sota-restore button").length'),0);
  await set("#sota-rules",before.rules);await set("#sota-refresh_minutes",String(before.refresh_minutes));await apply();
  await go("exceptions");await set("#sota-exception-rules",before.exceptions.rules);await apply();
  await go("network");assert.ok(await run(q("#sota-device")+'.value'));
  await send("Emulation.setDeviceMetricsOverride",{width:375,height:812,deviceScaleFactor:1,mobile:true});
  await go("lists");assert.ok(await run('document.documentElement.scrollWidth<=window.innerWidth'));
  console.log("Verified restore is limited to built-ins, Linux-to-OpenWrt navigation, mobile layout, and deletion through the panel.");
} finally {
  // Undo only exact test additions, even if verification was interrupted.
  const cur=await read(), next=structuredClone(cur);
  if(next.rules===newRoutes)next.rules=before.rules;
  if(next.exceptions.rules===more([before.bypass,before.exceptions.rules].filter(Boolean).join("\n"),exceptionLine)){next.exceptions.rules=before.exceptions.rules;next.bypass=before.bypass}
  if(next.refresh_minutes===interval)next.refresh_minutes=before.refresh_minutes;
  next.sources=next.sources.filter(s=>s!==extra);next.files=next.files.filter(f=>f.name!==file);
  if(JSON.stringify(cur)!==JSON.stringify(next)) {
    delete next.catalog;delete next.russian_default;
    await rpc("network.set",{network:next});
  }
  if(sock){sock.send(JSON.stringify({id:99999,method:"Browser.close"}));sock.close()}chrome.kill();
  await stopped;
  await fs.rm(home,{recursive:true,force:true,maxRetries:4,retryDelay:150});
  const after=await read();assert.equal(after.revision,before.revision,"Original routing revision must be restored");
  console.log("Original routing revision restored.");
}

