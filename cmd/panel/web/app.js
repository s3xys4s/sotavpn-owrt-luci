import {loadLocale} from "./i18n.js";

let language = "ru";
try { if (localStorage.getItem("sota.language")==="en") language="en"; } catch {}
const locale = await loadLocale(language);
const bind = (node, source) => locale.bind(node, source);
let lastMeasurement = null;
const el=id=>document.getElementById(id);
let state=null, busy=false, serial=0, measured=false, protocolEdited=false;
let endpoint="/rpc", openwrt=false, events=[];
async function rpc(method,params={}){
  const response=await fetch(endpoint,{method:"POST",headers:{"Content-Type":"application/json"},credentials:"omit",body:JSON.stringify({jsonrpc:"2.0",id:++serial,method,params})});
  if(!response.ok) throw new Error("Панель недоступна · HTTP "+response.status);
  const body=await response.json(); if(body.error)throw new Error(body.error.message);return body.result;
}
function controls(){
  const running=state?.core?.core_state>0,locked=state?.key_source==="env";
  el("connect").disabled=busy||!state?.core?.core_responding||running||!el("server").value;
  el("server").disabled=el("protocol").disabled=busy||running;
  const imported=el("server").value==="file";
  for(const option of el("protocol").querySelectorAll("[data-import]"))option.hidden=!imported;
  if(el("protocol").selectedOptions[0]?.hidden)el("protocol").value="auto";
  el("disconnect").disabled=busy||!(running||state?.provider_session||state?.want_tunnel||openwrt&&state?.core.policy_enabled);
  el("restart").disabled=busy||!state?.core?.core_responding||!el("server").value;
  el("refresh").disabled=busy||!state?.has_key;el("checkip").disabled=busy||!state;
  el("login").disabled=busy||locked;el("key").disabled=!!locked;el("import").disabled=busy;
}
function render(s){
  el("hwid").value=el("hwid").title=s.hwid || "";
  const previous=el("server").value;
  if(state && state.core.core_state!==s.core.core_state && measured) resetIP();
  state=s;bind(el("core"),s.core.core_responding?"Ядро доступно":"Ядро недоступно");el("core").classList.toggle("ok",s.core.core_responding);
  bind(el("connection"),!s.core.core_responding?"Нет связи с ядром":s.core.core_state===0?(openwrt&&!s.core.policy_enabled?"Правила маршрутизации выключены":"Туннель выключен"):"Туннель запущен");
  el("server").replaceChildren(bind(new Option("",""),"Выберите сервер"));
  for(const v of s.servers||[])el("server").add(new Option(v.name+(v.shortname?" · "+v.shortname:""),String(v.id)));
  if(s.imported)el("server").add(bind(new Option("","file"),"Импортированный профиль"));
  const choices=Array.from(el("server").options).map(o=>o.value);
  el("server").value=[previous,String(s.selected??""),s.imported?"file":"",choices[1]].find(v=>v&&choices.includes(v))||"";
  if(!protocolEdited||s.core.core_state>0)el("protocol").value=s.protocol||"auto";
  bind(el("key-info"),s.key_source==="env"?"Ключ задан окружением службы.":s.has_key?"Ключ сохранён":"Для подписки используется постоянный идентификатор устройства.");
  controls();
}
function resetIP(){measured=false;lastMeasurement=null;el("wan").textContent=el("tunnel").textContent="—";bind(el("measure"),openwrt?"Шлюз проверяет прямой выход и выход через туннель.":"Запрос через TUN выполняет отдельный клиент.")}
function notify(msg){bind(el("notice"),msg);el("notice").hidden=!msg}
async function action(fn,modal=false){
  if(busy)return;busy=true;controls();notify("");bind(el("modal-msg"),"");
  try{await fn()}catch(e){if(modal)bind(el("modal-msg"),e.message);else notify(e.message)}
  finally{try{render(await rpc("status"))}catch(e){state=null;notify(e.message)}busy=false;controls()}
}
el("account").onclick=()=>el("access").showModal();el("close").onclick=()=>el("access").close();
el("access").addEventListener("close",()=>{el("key").value="";el("file").value="";bind(el("modal-msg"),"")});
el("access").querySelector("form").onsubmit=e=>{e.preventDefault();el("login").click()};
el("server").onchange=controls;
el("protocol").onchange=()=>{protocolEdited=true};
el("login").onclick=()=>action(async()=>{const key=el("key").value.trim();if(!key)throw new Error("Введите ключ подписки");await rpc("account.setKey",{key});el("access").close();notify("Ключ принят. Выберите сервер для подключения.")},true);
el("import").onclick=()=>action(async()=>{const f=el("file").files[0];if(!f||f.size>900000)throw new Error("Выберите JSON-файл размером до 900 КБ");let profile;try{profile=JSON.parse(await f.text())}catch{throw new Error("Файл содержит некорректный JSON")}await rpc("profile.import",{profile});el("access").close();notify("Профиль сохранён")},true);
el("refresh").onclick=()=>action(async()=>{await rpc("server.list")});
function startTunnel(method){return action(async()=>{const value=el("server").value,protocol=el("protocol").value;await rpc(method,value==="file"?{protocol}:{id:Number(value),protocol});protocolEdited=false;resetIP()});}
el("connect").onclick=()=>startTunnel("tunnel.connect");
el("restart").onclick=()=>startTunnel("tunnel.restart");
el("disconnect").onclick=()=>action(async()=>{await rpc("tunnel.disconnect");resetIP()});
el("checkip").onclick=()=>action(async()=>{bind(el("measure"),"Проверяем оба выхода…");el("wan").textContent=el("tunnel").textContent="…";let r;try{r=await rpc("diagnostics.ip")}catch(e){resetIP();bind(el("measure"),"Проверку завершить не удалось.");throw e}displayMeasurement(r);});
function displayMeasurement(r){
  measured=true;lastMeasurement=r;
  for(const k of ["wan","tunnel"])el(k).textContent=r[k].ip||locale.t(r[k].status==="disconnected"?"Выключен":"Недоступен");
  const detail=r.tunnel.ip?(r.same_ip?"Оба маршрута дают одинаковый IP.":r.wan.ip?"Публичные адреса выходов различаются.":"Ответ через TUN получен."):"Ответ через TUN не получен.";
  el("measure").textContent=locale.t(detail)+" "+new Date(r.time).toLocaleTimeString(locale.language);
}
function showEvents(){
  const level=el("level").value;
  const rows=events.slice().reverse().filter(e=>!level||e.level===level).map(e=>{
    const row=document.createElement("div"), badge=document.createElement("strong"), time=document.createElement("time");
    badge.textContent=locale.level(e.level);badge.className="severity-"+e.level;
    time.textContent=new Date(e.time).toLocaleTimeString(locale.language);
    row.append(time,badge,document.createTextNode(locale.t(e.message)));return row;
  });
  el("events").replaceChildren(...rows);
  if(!rows.length)el("events").textContent=locale.t("Событий пока нет");
}
el("level").onchange=showEvents;
async function refreshEvents(){
  if(!el("pause").checked){
    try{const result=await rpc("logs");events=Array.isArray(result)?result:[];showEvents()}
    catch{el("events").textContent=locale.t("Журнал недоступен")}
  }
}

// Rebase a tab's edits onto changes saved in another tab or LuCI.
async function createRoutingForm(page) {
  const {routingEditor,writeRoutingChunks} = await import("/routing.js");
  const read = () => rpc("network.get");
  const save = async (draft,baseline,changes) => {
    const latest = await read();
    if (latest.revision !== baseline.revision) {
      for (const key of Object.keys(changes)) {
        const now = JSON.stringify(latest[key]);
        if (now !== JSON.stringify(baseline[key]) && now !== JSON.stringify(changes[key])) {
          throw Error("Настройки изменились. Загрузите заново перед сохранением");
        }
      }
      for (const key of Object.keys(draft)) {
        if (!Object.hasOwn(changes,key)) draft[key] = latest[key];
      }
    }
    await writeRoutingChunks(draft,(transfer,offset,chunk,final) =>
      rpc("network.writepart",{transfer,offset,chunk,final}));
    return read();
  };
  return routingEditor(read,save,locale,page);
}

function setupPages(remote) {
  if (!remote) return;
  const nav = el("page-nav");
  const links = Array.from(nav.querySelectorAll("[data-page]"));
  nav.hidden = false;
  if (!openwrt) {
    // List policies belong to the router selected by these links.
    for (const link of links) {
      if (link.dataset.page !== "status") link.href = location.pathname + link.hash;
    }
  }
  const panels = new Map();
  async function showPage() {
    const link = links.find(item => item.dataset.page === location.hash.slice(1)) || links[0];
    const page = link.dataset.page;
    const routing = page !== "status";
    if (routing && !openwrt) {
      location.replace(link.href);
      return;
    }
    for (const item of links) {
      if (item === link) item.setAttribute("aria-current","page");
      else item.removeAttribute("aria-current");
    }
    el("status-page").hidden = routing;
    el("network-card").hidden = !routing;
    for (const [id,panel] of panels) panel.hidden = id !== page;
    if (!routing) return;
    bind(el("routing-title"),link.dataset.i18n);
    if (panels.has(page)) {
      panels.get(page).querySelector("form")?.refreshIfClean();
      return;
    }
    const panel = document.createElement("div");
    panels.set(page,panel);
    const loading = bind(document.createElement("p"),"Загружаем настройки…");
    loading.setAttribute("role","status");
    panel.append(loading);
    el("network-editor").append(panel);
    try {
      const form = await createRoutingForm(page);
      locale.refresh(form);
      panel.replaceChildren(form);
    } catch (e) {
      bind(loading,e.message);
    }
  }
  window.addEventListener("hashchange",showPage);
  showPage();
}
async function init(){
  let remote=false;
  try{remote=(await (await fetch("/backends")).json()).openwrt===true}catch{}
  openwrt=remote&&new URLSearchParams(location.search).get("runtime")!=="linux";
  endpoint=openwrt?"/router-rpc":"/rpc";
  el("runtime-label").hidden=!remote;
  el("runtime").value=openwrt?"openwrt":"linux";
  el("runtime").onchange=()=>{location.href=location.pathname+(el("runtime").value==="linux"?"?runtime=linux":"")+"#status"};
  resetIP();
  setupPages(remote);
  await action(async()=>{const s=await rpc("status");render(s);if(s.has_key&&!s.servers?.length)await rpc("server.list")});
  await refreshEvents();
  setInterval(async()=>{if(busy||el("access").open)return;
    try{const s=await rpc("status");if(!busy)render(s)}
    catch{state=null;resetIP();bind(el("connection"),"Состояние неизвестно");bind(el("core"),"Нет связи");el("core").classList.remove("ok");controls()}
    await refreshEvents();
  },5000);
}
function selectLanguage(value) {
  locale.language=value;
  document.documentElement.lang=value;
  locale.refresh(document);
  for(const button of document.querySelectorAll("[data-language]")){
    button.setAttribute("aria-pressed", String(button.dataset.language===value));
  }
  if(lastMeasurement) displayMeasurement(lastMeasurement);
  showEvents();
}
for(const button of document.querySelectorAll("[data-language]")){
  button.onclick=()=>{
    const value=button.dataset.language;
    try {localStorage.setItem("sota.language",value)} catch {}
    selectLanguage(value);
  };
}
selectLanguage(language);
init();
