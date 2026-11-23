// Used by both the standalone panel and LuCI.
export async function writeRoutingChunks(value, writePart) {
  const data = new TextEncoder().encode(JSON.stringify(value));
  let transfer = "";
  for (let offset = 0; offset < data.length; offset += 12288) {
    const piece = data.subarray(offset, offset + 12288);
    const final = offset + piece.length === data.length;
    const result = await writePart(transfer, offset, btoa(String.fromCharCode(...piece)), final);
    if (!final) transfer = result.transfer;
  }
}

export async function routingEditor(read, save, locale, page="all") {
  const bind = (node, message, property) => locale.bind(node, message, property);
  const form = document.createElement("form");
  form.className = "sota-routing";
  const controls = {};
  function field(key, title, tag, hint) {
    const row = document.createElement("div");
    row.className = "cbi-value";
    const label = document.createElement("label");
    const input = document.createElement(tag);
    input.id = "sota-" + key;
    label.htmlFor = input.id;
    label.className = "cbi-value-title";
    bind(label, title);
    controls[key] = input;
    const group = document.createElement("div");
    group.className = "cbi-value-field";
    group.append(input);
    row.append(label, group);
    if (hint) {
      const p = document.createElement("small");
      bind(p, hint);
      p.className = "cbi-value-description";
      group.append(p);
    }
    form.append(row);
    return input;
  }
  const status = document.createElement("p");
  status.setAttribute("role", "status");
  let saved;
  let initialFields, busy = false;
  const editors = {};
  const onPage = name => page === "all" || page === name;
  function heading(title) {
    if (page === "all") form.append(bind(document.createElement("h3"), title));
  }
  if (onPage("network")) {
    field("enabled", "Включить правила маршрутизации", "input", "Применять выбранные маршруты к трафику устройств на входном интерфейсе.").type = "checkbox";
    field("device", "Входной интерфейс", "input").required = true;
    field("dns", "DNS через VPN", "input", "При включённом DoH этот IPv4 используется для поиска адреса DoH-сервера. Для запуска самого VPN используется DNS интерфейса WAN.").required = true;
    const doh = field("doh_url", "DNS over HTTPS", "input", "Укажите HTTPS-ссылку для шифрования клиентского DNS через VPN. Пустое поле включает обычный DNS.");
    doh.type = "url";
    doh.placeholder = "https://cloudflare-dns.com/dns-query";
    field("clients", "Устройства, для которых действуют правила", "textarea", "Укажите IPv4-адреса устройств или подсети. Оставьте поле пустым, чтобы правила действовали для всех устройств на этом интерфейсе.").rows = 2;
  }
  if (onPage("lists")) {
    heading("Списки маршрутизации");
    const direction = field("direction", "Куда направлять трафик", "select");
    direction.add(bind(new Option("", "wan"), "Совпадения напрямую (WAN), остальное через VPN"));
    direction.add(bind(new Option("", "vpn"), "Совпадения через VPN, остальное напрямую (WAN)"));
    const interval = field("refresh_minutes", "Интервал обновления ссылок, мин", "input");
    interval.type = "number";
    interval.min = "1";
    interval.max = "10080";
    interval.required = true;

    editors.lists = listEditor(false);
  }
  if (onPage("exceptions")) {
    heading("Исключения");
    form.append(bind(document.createElement("p"), "Совпадения с исключениями всегда идут напрямую через WAN."));
    editors.exceptions = listEditor(true);
  }
  function listEditor(exception) {
    let presets = [], originalFile;
    const restore = document.createElement("div");
    restore.className = "sota-restore";
    const presetTitle = p => p.name + " · " + (p.kind === "domains" ? "Домены" : "IPv4 / CIDR");
    const rules = field(exception ? "exception-rules" : "rules", exception ? "Ручные исключения" : "Ручные правила", "textarea",
      "По одному правилу на строку. example.com — само имя, *.example.com — его поддомены. Можно указать оба.");
    rules.spellcheck = false;
    rules.rows = 7;
    bind(rules, "Домен определяется по IPv4 из ответов DNS шлюза. Сторонний DoH может обойти это правило.", "title");
    rules.placeholder = "1.1.1.1/32\n192.168.1.1/24\ngoogle.com\n*.microsoft.com";
    const sources = document.createElement("fieldset");
    sources.className = "sota-sources";
    const legend = document.createElement("legend");
    bind(legend, "Списки по ссылкам");
    const hint = document.createElement("p");
    bind(hint, "Шлюз скачивает новый список при сохранении и обновляет его по расписанию. Если источник недоступен, действует последняя рабочая копия.");
    const rows = document.createElement("div");
    const add = document.createElement("button");
    add.type = "button";
    add.className = "secondary cbi-button";
    bind(add, "+ Добавить список");
    function removeButton(row, focus, title) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "secondary cbi-button";
      remove.textContent = "−";
      bind(remove, title, "title");
      bind(remove, title, "aria-label");
      remove.onclick = () => { row.remove(); refreshRestore(); focus.focus(); };
      return remove;
    }
    function addSource(value) {
      const row = document.createElement("div");
      row.className = "sota-source-row";
      const input = document.createElement("input");
      input.type = "url";
      input.required = true;
      input.spellcheck = false;
      bind(input, "Ссылка на текстовый список", "aria-label");
      input.placeholder = "https://example.org/routes.txt";
      input.value = value;
      const caption = document.createElement("small");
      const updateLabel = () => { const preset = presets.find(p => p.url === input.value.trim()); bind(caption, preset ? presetTitle(preset) : ""); caption.hidden = !preset; };
      input.oninput = () => { updateLabel(); refreshRestore(); };
      updateLabel();
      row.append(caption, input, removeButton(row, add, "Удалить список"));
      rows.append(row);
      return input;
    }
    add.onclick = () => addSource("").focus();
    sources.append(legend, hint, rows, add);
    form.append(sources);
    const files = document.createElement("fieldset");
    files.className = "sota-sources sota-files";
    const fileLegend = document.createElement("legend");
    bind(fileLegend, "Списки из файлов");
    const fileHint = document.createElement("p");
    bind(fileHint, "Шлюз хранит копию каждого файла и объединяет его правила со ссылками и ручными правилами.");
    const fileRows = document.createElement("div");
    const fileData = new WeakMap();
    const picker = document.createElement("input");
    picker.type = "file";
    picker.multiple = true;
    picker.accept = ".txt,.list,.yaml,.yml,text/plain";
    picker.hidden = true;
    const addFile = document.createElement("button");
    addFile.type = "button";
    addFile.className = "secondary cbi-button";
    bind(addFile, "+ Добавить файл");
    addFile.onclick = () => picker.click();
    function addFileRow(data) {
      const row = document.createElement("div");
      row.className = "sota-source-row sota-file-row";
      const name = document.createElement("span");
      name.textContent = data.name;
      fileData.set(row, data);
      row.append(name, document.createTextNode(" "), removeButton(row, addFile, "Удалить файл"));
      fileRows.append(row);
    }
    files.append(fileLegend, fileHint, fileRows, addFile, picker);
    form.append(files);
    picker.onchange = async () => {
      const selected = Array.from(picker.files);
      setBusy(true);
      try {
        if (fileRows.children.length + selected.length > 32) throw Error("Можно добавить до 32 файлов");
        if (selected.reduce((bytes, file) => bytes + file.size, 0) > 8 * 1024 * 1024) throw Error("Общий размер файлов превышает 8 МиБ");
        const pending = [];
        for (const file of selected) {
          if (file.size > 2 * 1024 * 1024) throw Error("Нужен файл в UTF-8 размером до 2 МиБ");
          const bytes = await file.arrayBuffer();
          let text;
          try { text = new TextDecoder("utf-8", {fatal:true}).decode(bytes); }
          catch { throw Error("Нужен файл в UTF-8 размером до 2 МиБ"); }
          pending.push({name:file.name, text});
        }
        for (const data of pending) addFileRow(data);
        bind(status, "Файлы добавлены. Нажмите «Применить правила».");
      } catch(e) { bind(status, e.message); }
      finally { picker.value = ""; setBusy(false); }
    };
    function refreshRestore() {
      restore.replaceChildren();
      function offer(title, action) {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "sota-restore-button cbi-button";
        bind(button, "Вернуть " + title);
        button.onclick = () => { action(); refreshRestore(); };
        restore.append(button);
      }
      const urls = new Set(Array.from(rows.querySelectorAll("input"), el => el.value.trim()));
      for (const p of presets) if (!urls.has(p.url)) offer(presetTitle(p), () => addSource(p.url));
      if (originalFile && !Array.from(fileRows.children, el => fileData.get(el)).some(f => f.name === originalFile.name && f.hash === originalFile.hash)) {
        offer("Российские сайты", () => addFileRow(originalFile));
      }
    }
    form.append(restore);
    return {
      populate(data, catalog, file) {
        presets = catalog;
        originalFile = file;
        rules.value = data.rules || "";
        rows.replaceChildren();
        fileRows.replaceChildren();
        for (const url of data.sources || []) addSource(url);
        for (const file of data.files || []) addFileRow(file);
        refreshRestore();
      },
      value() {
        return {rules:rules.value, sources:Array.from(rows.querySelectorAll("input"), el=>el.value.trim()), files:Array.from(fileRows.children, el=>fileData.get(el))};
      }
    };
  }
  const apply = document.createElement("button");
  bind(apply, "Применить правила");
  apply.className = "primary cbi-button cbi-button-apply";
  const reload = document.createElement("button");
  reload.type = "button";
  bind(reload, "Прочитать сохранённые настройки");
  reload.className = "secondary cbi-button";
  form.append(apply, document.createTextNode(" "), reload, status);
  function populate(data) {
    saved = data;
    if (controls.enabled) {
      controls.enabled.checked = data.enabled;
      for (const key of ["device","dns","doh_url","clients"]) controls[key].value = data[key] || "";
    }
    if (editors.lists) {
      controls.direction.value = data.direction;
      controls.refresh_minutes.value = data.refresh_minutes;
      editors.lists.populate(data, data.catalog, null);
    }
    if (editors.exceptions) {
      const group = data.exceptions;
      editors.exceptions.populate({...group, rules:[data.bypass,group.rules].filter(Boolean).join("\n")}, [], data.russian_default);
    }
    initialFields = editedFields();
  }
  function setBusy(value) {
    busy = value;
    for (const input of form.elements) input.disabled = busy;
    apply.disabled = busy || !saved;
  }
  async function update(operation, label) {
    setBusy(true);
    try { populate(await operation()); bind(status, label); }
    catch(e) { bind(status, e.message); }
    finally { setBusy(false); }
  }
  reload.onclick = () => update(read, "Настройки загружены");
  function editedFields() {
    const value = {};
    if (controls.enabled) {
      value.enabled=controls.enabled.checked;
      for (const key of ["device","dns","doh_url","clients"]) value[key]=controls[key].value;
    }
    if (editors.lists) {
      Object.assign(value,editors.lists.value());
      value.direction=controls.direction.value;
      value.refresh_minutes=controls.refresh_minutes.valueAsNumber;
    }
    if (editors.exceptions) {
      value.exceptions=editors.exceptions.value();
      if (value.exceptions.rules === [saved.bypass,saved.exceptions.rules].filter(Boolean).join("\n")) value.exceptions.rules=saved.exceptions.rules;
      else value.bypass="";
    }
    return value;
  }
  function changedFields() {
    return Object.fromEntries(Object.entries(editedFields()).filter(
      ([key,val]) => JSON.stringify(val) !== JSON.stringify(initialFields[key])
    ));
  }
  form.refreshIfClean = () => {
    if (!busy && (!saved || !Object.keys(changedFields()).length)) {
      return update(read, "");
    }
  };
  form.onsubmit = ev => {
    ev.preventDefault();
    if (!saved || busy) return;
    const changes = changedFields();
    const value = {...saved, ...changes};
    delete value.catalog;
    delete value.russian_default;
    return update(() => save(value, saved, changes), "Правила применены");
  };
  await update(read, "");
  return form;
}

