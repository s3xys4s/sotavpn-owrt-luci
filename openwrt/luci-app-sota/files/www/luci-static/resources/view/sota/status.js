'use strict';
'require view';
'require rpc';
'require ui';
'require poll';
'require sota.locale as language';
'require sota.toolbar as toolbar';

var api = {};
Object.entries({status:[], servers:[], key:['key'], connect:['id','protocol'], disconnect:[], restart:['id','protocol'], diagnostics:[], logs:[]}).forEach(function(entry) {
	api[entry[0]] = rpc.declare({object:'sota', method:entry[0], params:entry[1], reject:true, expect:{'':{}}});
});
async function call(name, ...args) {
	var reply = await api[name](...args);
	if (reply.error) throw new Error(reply.error);
	return reply.data;
}
return view.extend({
	handleSave: null, handleSaveApply: null, handleReset: null,
	load: function() { return language.load(); },
	render: function(locale) {
		const t = value => locale.t(value);
		var server = E('select'), mode = E('select', {}, ['auto','vless','hysteria2'].map(function(value,i) {
			return E('option',{value:value},t(['Авто','VLESS','Hysteria2'][i]));
		}));
		var hwid = E('input',{id:'sota-hwid',type:'text',dir:'ltr',disabled:'disabled','class':'cbi-input-text',style:'width:100%;max-width:68ch'});
		var status = E('p'), account = E('p'), ip = E('p'), logs = E('div', {'aria-live':'off'});
		var filter = E('select', {change:drawLogs}, ['Все','info','warn','error'].map(function(v) { return E('option',{value:v},locale.level(v)); }));
		var pause = E('input',{type:'checkbox'}), events = [], busy = false, triedLoading = false, lastState;
		var go = button('Старт',async function(){await call('connect',Number(server.value),mode.value);});
		var stop = button('Стоп',()=>call('disconnect'));
		var restart = button('Перезапуск',()=>call('restart',Number(server.value),mode.value));
		function button(title, fn) {
			return E('button',{'class':'cbi-button',click:async function(ev){
				ev.preventDefault(); if(busy)return; busy=true;
				try { await fn(); } catch(e){ui.addNotification(null,E('p',{},t(e.message)),'error');}
				finally { busy=false; await refresh(); }
			}},t(title));
		}
		function drawLogs() {
			logs.replaceChildren(...events.slice().reverse().filter(e=>filter.value==='Все'||filter.value===e.level).map(e=>
				E('div',{},[E('time',{},new Date(e.time).toLocaleString(locale.language)+' '),E('strong',{style:'color:'+({info:'#287994',warn:'#986300',error:'#c02b35'}[e.level]||'inherit')},locale.level(e.level)+' '),document.createTextNode(t(e.message))])));
		}
		async function refresh() {
			try {
				var s=await call('status'), running=s.core.core_state===2;
				status.textContent=t(!s.core.core_responding?'Ядро недоступно':running?'Туннель подключён':s.core.policy_enabled?'Туннель выключен':'Правила маршрутизации выключены');
				hwid.value=hwid.title=s.hwid || '';
				account.textContent=t(s.has_key?'Ключ сохранён':'Ключ не задан');
				var list=s.servers||[], previous=server.value;
				if(s.has_key && !list.length && !triedLoading) {
					triedLoading=true;
					try { list=await call('servers'); }
					catch(e) { ui.addNotification(null,E('p',{},t(e.message)),'error'); }
				}
				server.replaceChildren(...list.map(v=>E('option',{value:String(v.id)},v.name)));
				if(list.some(v=>String(v.id)===previous))server.value=previous;
				else if(s.selected!=null)server.value=String(s.selected);
				if(lastState===undefined||running)mode.value=s.protocol;
				if(lastState!==s.core.core_state)ip.textContent='';
				lastState=s.core.core_state;
				server.disabled=mode.disabled=running;
				go.disabled=running||!server.value;
				restart.disabled=!server.value;
				stop.disabled=!running&&!s.provider_session&&!s.want_tunnel&&!s.core.policy_enabled;
				if(!pause.checked){events=(await call('logs'))||[];drawLogs();}
			} catch(e){ status.textContent=t('Служба недоступна');go.disabled=restart.disabled=false;stop.disabled=true; }
		}
		var access=button('Ключ подписки',async function(){
			var k=E('input',{type:'password',autocomplete:'off'});
			ui.showModal(t('Ключ подписки'),[k,E('button',{'class':'cbi-button',click:async function(){
				try{await call('key',k.value);k.value='';ui.hideModal();await refresh();}catch(e){ui.addNotification(null,E('p',{},t(e.message)),'error');}
			}},t('Сохранить')),E('button',{'class':'cbi-button',click:ui.hideModal},t('Отмена'))]);
		});
		var probe=button('Проверить IP',async function(){
			var r=await call('diagnostics');ip.textContent='WAN: '+(r.wan.ip||t('недоступен'))+' · '+t('Через TUN')+': '+(r.tunnel.ip||t(r.tunnel.status==='disconnected'?'выключен':'недоступен'));
		});
		poll.add(()=>busy?Promise.resolve():refresh(),5);
		return E('div',{},[E('link',{rel:'stylesheet',href:language.resource('sota/routes.css')}),E('h2',{},'Sota Gateway'),toolbar.row(t,[go,stop,restart]),status,account,access,
			E('p',{},[E('label',{for:'sota-hwid'},t('HWID устройства')),E('br'),hwid]),
			E('p',{},[E('label',{},[t('Сервер')+' ',server]),' ',button('Обновить серверы',()=>call('servers')),' ',E('label',{},[t('Протокол')+' ',mode])]),
			probe,ip,E('h3',{},t('Последние события')),E('label',{},[t('Уровень')+' ',filter]),' ',E('label',{},[pause,' '+t('Пауза')]),logs]);
	}
});
