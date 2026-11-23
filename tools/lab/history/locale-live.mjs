import http from 'node:http';
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import path from 'node:path';
import assert from 'node:assert/strict';
import {setTimeout as delay} from 'node:timers/promises';
const home=await fs.mkdtemp(path.resolve('cache/luci-check-'));
const [mode, pref, browserLang='en'] = process.argv.slice(2);
const addr=mode==='main'?'http://192.168.1.88:8080/':'http://192.168.1.88:8081/cgi-bin/luci/admin/services/sota/status';
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
  if(mode==='main'){
    await until('document.querySelector("#sota-rules") && document.querySelector("#core").textContent==="Ядро доступно"');
    await check('document.documentElement.lang==="ru" && document.querySelector("[data-language=ru]").getAttribute("aria-pressed")==="true"');
    await run('document.querySelector("#sota-rules").value+="\\nlocale-test.example";document.querySelector(".sota-sources>button").click();document.querySelector("[data-language=en]").click()');
    await check('document.documentElement.lang==="en" && document.querySelector("#connect").textContent==="Connect" && document.querySelector("#sota-rules").value.endsWith("locale-test.example")');
    await check('document.querySelector(".sota-sources legend").textContent==="Lists from URLs" && document.querySelector(".sota-source-row:last-child button").title==="Remove list" && document.querySelector("label[for=sota-rules]").textContent==="Manual rules"');
    await check('document.querySelector("#level [value=warn]").textContent==="Warning" && document.querySelector(".languages").getAttribute("aria-label")==="Interface language"');
    await run('document.querySelector("#account").click(); document.querySelector("#import").click()');
    await until('document.querySelector("#modal-msg").textContent==="Choose a JSON file up to 900 KB"');
    await check('document.querySelector("label[for=key]").textContent==="Subscription key" && document.querySelector("#key").placeholder==="Enter your Sota key"');
    await run('document.querySelector("#close").click()');
    await until('!document.querySelector("#checkip").disabled');
    await run('document.querySelector("#checkip").click()');
    await until('/^\\d+\\./.test(document.querySelector("#wan").textContent) && /^\\d+\\./.test(document.querySelector("#tunnel").textContent)');
    await check('document.querySelector("#measure").textContent.includes("public IP")');
    await run('document.querySelector("[data-language=ru]").click()');
    await check('document.querySelector("#measure").textContent.includes("адреса") || document.querySelector("#measure").textContent.includes("одинаковый")');
    await run('document.querySelector("[data-language=en]").click()');
    await send('Page.reload',{ignoreCache:false});
    await until('document.querySelector("#sota-rules") && document.querySelector("#core").textContent==="VPN core available"');
    await check('!document.querySelector("#sota-rules").value.includes("locale-test.example") && document.querySelector("[data-language=en]").getAttribute("aria-pressed")==="true"');
    await nav(addr+'?runtime=linux');
    await until('document.documentElement.lang==="en" && document.querySelector("#core")?.textContent==="VPN core available"');
    await check('document.querySelector("#runtime").value==="linux" && document.querySelector("#connect").textContent==="Connect"');
    await run('document.querySelector("[data-language=ru]").click()');
    await check('document.querySelector("#connect").textContent==="Подключить"');
    console.log("Standalone: RU/EN, unsaved rules, URL editor, translated modal and errors, IP check, persistence, Linux view OK");
  } else {
    await send('Network.enable');await send('Network.setExtraHTTPHeaders',{headers:{'Accept-Language':browserLang==='ru'?'ru-RU,ru;q=0.9':'en-US,en;q=0.9'}});
    await nav(addr);
    await until('document.querySelector("input[name=luci_username]")');
    await run('document.querySelector("input[name=luci_username]").value="root";document.querySelector("input[name=luci_username]").form.submit()');
    const en=pref==='en';
    await until('document.body.innerText.includes('+JSON.stringify(en?'Key saved':'Ключ сохранён')+')');
    await check('document.documentElement.lang.startsWith('+JSON.stringify(pref)+')');
    await check('document.body.innerText.includes('+JSON.stringify(en?'Tunnel connected':'Туннель подключён')+') && document.body.innerText.includes('+JSON.stringify(en?'Recent events':'Последние события')+')');
    await check('!document.querySelector("[data-language]")');
    const menu=await run('Array.from(document.querySelectorAll("a"),a=>[a.getAttribute("href"),a.textContent.trim()]).filter(([href,name])=>href?.includes("/services/sota/") && name).map(x=>x[1])');
    assert(menu.includes(en?'Routing and DNS':'Маршрутизация и DNS'),JSON.stringify(menu));
    assert(menu.includes(en?'VPN connection':'Подключение'),JSON.stringify(menu));
    await nav(addr.replace('/status','/network'));
    await until('document.querySelector("#sota-rules")');
    await check('document.querySelector("label[for=sota-rules]").textContent==='+JSON.stringify(en?'Manual rules':'Ручные правила'));
    await check('document.querySelector(".sota-sources legend").textContent==='+JSON.stringify(en?'Lists from URLs':'Списки по ссылкам'));
    await run('document.querySelector(".sota-sources>button").click()');
    await check('document.querySelector(".sota-source-row:last-child button").title==='+JSON.stringify(en?'Remove list':'Удалить список'));
    console.log("LuCI "+pref+" (browser "+browserLang+"): status, navigation, routing editor and list controls OK");
  }
}finally{if(sock){sock.send(JSON.stringify({id:99999,method:'Browser.close'}));sock.close()}chrome.kill()}

