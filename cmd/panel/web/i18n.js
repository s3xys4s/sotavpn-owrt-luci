// Shared by the standalone panel and the LuCI views. Only application messages
// are bound here. Provider names, URLs, addresses and form values stay verbatim.
export async function loadLocale(language) {
  const url = new URL("messages.json", import.meta.url);
  url.search = new URL(import.meta.url).search;
  const response = await fetch(url);
  if (!response.ok) throw new Error("Could not load Sota translations");
  const english = await response.json();
  // Controller errors and historical events may include nested messages,
  // protocol names, HTTP codes and rule numbers.
  const escape = value => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const fragments = new RegExp(Object.keys(english).sort((a,b) => b.length-a.length).map(escape).join("|"), "g");
  const attrs = {textContent:"data-i18n", title:"data-i18n-title", placeholder:"data-i18n-placeholder", "aria-label":"data-i18n-label"};
  const locale = {
    language,
    level(value) {
      const labels = {info:"Сведения", warn:"Предупреждение", error:"Ошибка"};
      return this.t(labels[value] || value);
    },
    t(source) {
      if (!source || /^ru(?:-|$)/i.test(this.language)) return source;
      return Object.hasOwn(english, source) ? english[source] : source.replace(fragments, key => english[key]);
    },
    bind(node, source, property="textContent") {
      node.setAttribute(attrs[property], source);
      const value = this.t(source);
      if (property==="textContent") node.textContent=value;
      else node.setAttribute(property,value);
      return node;
    },
    refresh(root) {
      for (const [property,attr] of Object.entries(attrs)) {
        for (const node of root.querySelectorAll("["+attr+"]")) {
          const source=node.getAttribute(attr) || (property==="textContent"?node.textContent:node.getAttribute(property)) || "";
          this.bind(node,source,property);
        }
      }
    }
  };
  return locale;
}
