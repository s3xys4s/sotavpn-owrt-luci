package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const routeListFile = "/etc/sota/route-list.txt"
const routeRuntime = "/var/run/sota/"
const listLimit = 256 * 1024

type networkSettings struct {
	RussianDefault ruleFileInput      `json:"russian_default"`
	Catalog        []servicePreset    `json:"catalog,omitempty"`
	Exceptions     *exceptionSettings `json:"exceptions,omitempty"`
	Enabled        bool               `json:"enabled"`
	Device         string             `json:"device"`
	DNS            string             `json:"dns"`
	DoHURL         string             `json:"doh_url,omitempty"`
	Clients        string             `json:"clients"`
	Bypass         string             `json:"bypass"`
	Direction      string             `json:"direction"`
	Rules          string             `json:"rules"`
	Sources        []string           `json:"sources"`
	Files          []ruleFileInput    `json:"files"`
	RefreshMinutes int                `json:"refresh_minutes"`
	Revision       string             `json:"revision"`
	document       routeDocument
	storedHash     string
}

var deviceName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,14}$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

var dohURLChars = regexp.MustCompile(`^https://[a-zA-Z0-9.:/?&=%_~+,-]+$`)

func validDoH(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 1024 || !dohURLChars.MatchString(raw) || u.Path == "" || u.Hostname() == "" || len(u.Hostname()) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.ToLower(u.Hostname()), ".") {
		if !dnsLabel.MatchString(label) || strings.Contains(label, "_") {
			return false
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return false
		}
	}
	return true
}

func ipv4Network(s string) (string, bool) {
	if a, e := netip.ParseAddr(s); e == nil && a.Is4() {
		return a.String(), true
	}
	if p, e := netip.ParsePrefix(s); e == nil && p.Addr().Is4() {
		if p.Bits() == 32 {
			return p.Addr().String(), true
		}
		return p.Masked().String(), true
	}
	return "", false
}

type routeList struct{ ips, domains []string }

func parseList(text string) (routeList, error) {
	if len(text) > listLimit {
		return routeList{}, errors.New("Максимальный размер ручных правил — 256 КиБ")
	}
	return parseEntries(text)
}

func parseEntries(text string) (routeList, error) {
	var list routeList
	seen := map[string]bool{}
	for i, raw := range strings.Split(strings.TrimPrefix(text, "\ufeff"), "\n") {
		s := strings.TrimSpace(raw)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "!") || s == "payload:" {
			continue
		}
		if j := strings.Index(s, " #"); j >= 0 {
			s = strings.TrimSpace(s[:j])
		}
		s = strings.TrimSpace(strings.TrimPrefix(s, "- "))
		if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
			s = s[1 : len(s)-1]
		}
		kind := ""
		fields := strings.Split(s, ",")
		if len(fields) > 1 {
			kind = strings.ToUpper(strings.TrimSpace(fields[0]))
			s = strings.TrimSpace(fields[1])
			if len(fields) > 2 && !(kind == "IP-CIDR" && len(fields) == 3 && strings.TrimSpace(fields[2]) == "no-resolve") {
				return list, fmt.Errorf("Строка %d: лишние поля правила", i+1)
			}
		}
		if ip, ok := ipv4Network(s); ok && (kind == "" || kind == "IP-CIDR") {
			if !seen[ip] {
				list.ips = append(list.ips, ip)
				seen[ip] = true
			}
			continue
		}
		prefix := ""
		switch kind {
		case "DOMAIN":
			prefix = "-."
		case "DOMAIN-SUFFIX":
		case "":
			if strings.HasPrefix(s, `\*.`) {
				s = s[1:]
			}
			switch {
			case strings.HasPrefix(s, "||") && strings.HasSuffix(s, "^"):
				s = s[2 : len(s)-1]
			case strings.HasPrefix(s, "*."):
				prefix = "*."
				s = s[2:]
			case strings.HasPrefix(s, "+."):
				s = s[2:]
			default:
				prefix = "-."
			}
		default:
			return list, fmt.Errorf("Строка %d: неподдерживаемый тип правила", i+1)
		}
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		labels := strings.Split(s, ".")
		if len(s) > 249 || (len(labels) < 2 && prefix == "-.") || strings.Trim(labels[len(labels)-1], "0123456789") == "" {
			return list, fmt.Errorf("Строка %d: ожидается IPv4, CIDR или домен", i+1)
		}
		for _, label := range labels {
			if !dnsLabel.MatchString(label) {
				return list, fmt.Errorf("Строка %d: некорректное доменное имя", i+1)
			}
		}
		name := prefix + s
		if !seen[name] {
			list.domains = append(list.domains, name)
			seen[name] = true
		}
	}
	sort.Strings(list.ips)
	sort.Strings(list.domains)
	return list, nil
}

// Earlier releases interpreted unadorned names as suffixes. Make that intent
// explicit when opening a saved pre-source list, preserving comments and YAML.
var legacyBare = regexp.MustCompile(`(?m)^([ \t]*(?:-[ \t]+)?['"]?)([A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z0-9-]+\.?)(['"]?[ \t]*(?:#[^\r\n]*)?\r?)$`)

func migrateManual(text string) string {
	if strings.HasPrefix(text, "\ufeff") {
		return "\ufeff" + migrateManual(strings.TrimPrefix(text, "\ufeff"))
	}
	return legacyBare.ReplaceAllStringFunc(text, func(line string) string {
		m := legacyBare.FindStringSubmatch(line)
		if _, ok := ipv4Network(m[2]); ok {
			return line
		}
		return m[1] + "+." + m[2] + m[3]
	})
}

func readRuleText(hash string) (string, error) {
	path := routeListFile
	if hash != "" {
		if b, err := hex.DecodeString(hash); err != nil || len(b) != sha256.Size {
			return "", errors.New("Некорректная ссылка на список")
		}
		path += "." + hash
	}
	b, err := os.ReadFile(path)
	if hash == "" && os.IsNotExist(err) {
		return "", nil
	}
	if err == nil && hash != "" && fmt.Sprintf("%x", sha256.Sum256(b)) != hash {
		return "", errors.New("Повреждён файл списка")
	}
	return string(b), err
}

func compileRoutes() error {
	n, err := getNetwork()
	if err != nil {
		return err
	}
	rules, err := n.document.combined()
	if err != nil {
		return err
	}
	exclusions, err := n.document.Exceptions.combined()
	if err != nil {
		return err
	}
	var nft strings.Builder
	for _, group := range []struct {
		set string
		ips []string
	}{{"listed", rules.ips}, {"bypass", exclusions.ips}} {
		for i := 0; i < len(group.ips); i += 128 {
			end := min(i+128, len(group.ips))
			fmt.Fprintf(&nft, "add element inet sota_guard %s { %s }\n", group.set, strings.Join(group.ips[i:end], ", "))
		}
	}
	if err := atomicFile(routeRuntime+"list.nft", []byte(nft.String())); err != nil {
		return err
	}
	if err := atomicFile(routeRuntime+"bypass.dns", domainSetConfig(exclusions.domains, "bypass_resolved")); err != nil {
		return err
	}
	return atomicFile(routeRuntime+"list.dns", domainSetConfig(rules.domains, "resolved"))
}

func getNetwork() (networkSettings, error) {
	var n networkSettings
	values := map[string]string{}
	b, err := exec.Command("uci", "-q", "show", "sota.main").Output()
	if err != nil {
		return n, errors.New("Не удалось прочитать настройки UCI")
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimPrefix(k, "sota.main.")] = strings.ReplaceAll(v, "'", "")
		}
	}
	n.Enabled = values["enabled"] == "1"
	n.Device = values["protected_device"]
	n.DNS = values["dns"]
	n.DoHURL = values["doh_url"]
	n.Clients = strings.Join(strings.Fields(values["clients"]), "\n")
	n.Bypass = strings.Join(strings.Fields(values["bypass"]), "\n")
	n.Direction = values["list_direction"]
	if n.Direction == "" {
		n.Direction = "wan"
	}
	n.storedHash = values["rule_hash"]
	raw, err := readRuleText(n.storedHash)
	if err != nil {
		return n, err
	}
	if values["rule_format"] == "2" {
		if err = json.Unmarshal([]byte(raw), &n.document); err != nil {
			return n, errors.New("Не удалось прочитать сохранённые списки")
		}
	} else {
		n.document.Manual = migrateManual(raw)
	}
	n.populateSources()
	n.Revision = n.revision()
	return n, nil
}

func (n *networkSettings) populateSources() {
	n.Catalog = servicePresets
	n.RussianDefault = russianFile.reference()
	n.Exceptions = n.document.Exceptions.public()
	n.RefreshMinutes = n.document.RefreshMinutes
	if n.RefreshMinutes == 0 {
		n.RefreshMinutes = defaultRefreshMinutes
	}
	n.Files = make([]ruleFileInput, 0, len(n.document.Files))
	for _, f := range n.document.Files {
		n.Files = append(n.Files, f.reference())
	}
	n.Rules = n.document.Manual
	n.Sources = make([]string, 0, len(n.document.Lists))
	for _, source := range n.document.Lists {
		n.Sources = append(n.Sources, source.URL)
	}
}

// A background download changes the cached document, but an open editor still
// describes the same configuration. Only user-editable settings affect its revision.
func (n networkSettings) revision() string {
	n.Revision = ""
	b, _ := json.Marshal(n)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func normalizedIPs(text string) (string, error) {
	var ips []string
	seen := map[string]bool{}
	for _, v := range strings.Fields(text) {
		s, ok := ipv4Network(v)
		if !ok {
			return "", errors.New("Введите IPv4-адреса или подсети CIDR")
		}
		if !seen[s] {
			ips = append(ips, s)
			seen[s] = true
		}
	}
	return strings.Join(ips, "\n"), nil
}

func persistNetwork(n *networkSettings) error {
	var batch strings.Builder
	flag := "0"
	if n.Enabled {
		flag = "1"
	}
	fmt.Fprintf(&batch, "set sota.main.enabled=%s\nset sota.main.protected_device=%s\nset sota.main.dns=%s\nset sota.main.list_direction=%s\n", flag, n.Device, n.DNS, n.Direction)
	fmt.Fprintf(&batch, "set sota.main.doh_url='%s'\n", n.DoHURL)
	for _, item := range []struct{ k, v string }{{"clients", n.Clients}, {"bypass", n.Bypass}} {
		fmt.Fprintf(&batch, "delete sota.main.%s\n", item.k)
		for _, v := range strings.Fields(item.v) {
			fmt.Fprintf(&batch, "add_list sota.main.%s=%s\n", item.k, v)
		}
	}
	data, err := json.Marshal(n.document)
	if err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	path := routeListFile + "." + hash
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := atomicFile(path, data); err != nil {
			return err
		}
	}
	fmt.Fprintf(&batch, "set sota.main.rule_hash=%s\nset sota.main.rule_format=2\ncommit sota\n", hash)
	// A private delta directory keeps concurrent system-wide UCI edits separate.
	dir, err := os.MkdirTemp("", "sota-uci-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command("uci", "-q", "-t", dir, "batch")
	cmd.Stdin = strings.NewReader(batch.String())
	if err := cmd.Run(); err != nil {
		return err
	}
	n.storedHash = hash
	return nil
}

func applyNetwork() error {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, policyTool, "apply").Run(); err != nil {
		return err
	}
	return exec.CommandContext(ctx, "/etc/init.d/sota", "reload").Run()
}

func (a *app) setNetwork(ctx context.Context, n networkSettings) (networkSettings, error) {
	old, err := getNetwork()
	if err != nil {
		return old, err
	}
	if n.Revision != old.Revision {
		return old, errors.New("Настройки изменились. Загрузите заново перед сохранением")
	}
	ip, e := netip.ParseAddr(n.DNS)
	if e != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsLoopback() {
		return old, errors.New("Введите IPv4-адрес внешнего DNS")
	}
	if !validDoH(n.DoHURL) {
		return old, errors.New("Укажите HTTPS-адрес DoH-сервера с именем хоста и путём")
	}
	if !deviceName.MatchString(n.Device) || n.Device == "lo" || n.Device == "sota0" {
		return old, errors.New("Некорректный входной интерфейс")
	}
	if _, e := net.InterfaceByName(n.Device); e != nil {
		return old, errors.New("Входной интерфейс отсутствует")
	}
	if n.Direction != "wan" && n.Direction != "vpn" {
		return old, errors.New("Выберите маршрут списка")
	}
	if n.Clients, err = normalizedIPs(n.Clients); err != nil {
		return old, err
	}
	if n.Bypass, err = normalizedIPs(n.Bypass); err != nil {
		return old, err
	}
	if _, err = parseList(n.Rules); err != nil {
		return old, err
	}
	if n.Sources == nil {
		n.Sources = old.Sources
	}
	if n.RefreshMinutes == 0 {
		n.RefreshMinutes = old.RefreshMinutes
	}
	if n.RefreshMinutes < 1 || n.RefreshMinutes > 10080 {
		return old, errors.New("Укажите интервал от 1 до 10080 минут")
	}
	files, err := resolveRuleFiles(n.Files, old.document.Files)
	if err != nil {
		return old, err
	}
	lists, err := loadLists(ctx, listClient, n.Sources, old.document.Lists)
	if err != nil {
		return old, err
	}
	exceptions, err := prepareExceptions(ctx, n.Exceptions, old.document.Exceptions)
	if err != nil {
		return old, err
	}
	n.document = routeDocument{DefaultsInstalled: old.document.DefaultsInstalled, Exceptions: exceptions, Manual: n.Rules, Lists: lists, Files: files, RefreshMinutes: n.RefreshMinutes}
	n.populateSources()
	if old.Enabled && !n.Enabled {
		if err := a.setWanted(false); err != nil {
			return old, err
		}
		if err := a.stopLocal(); err != nil {
			return old, err
		}
		if err := a.closeProvider(ctx); err != nil {
			a.record("warn", "Не удалось закрыть сессию Sota")
		}
	}
	saved, err := commitNetwork(old, n, true)
	if err == nil && (old.RefreshMinutes != saved.RefreshMinutes || (len(old.Sources)+len(old.document.Exceptions.Lists) == 0 && len(saved.Sources)+len(saved.document.Exceptions.Lists) > 0)) {
		a.router.scheduleRefresh(saved.RefreshMinutes)
	}
	return saved, err
}

func commitNetwork(old, n networkSettings, apply bool) (networkSettings, error) {
	var saved networkSettings
	var err error
	if err = persistNetwork(&n); err == nil {
		saved, err = getNetwork()
		if err == nil {
			if saved.storedHash != n.storedHash || saved.Revision != n.revision() {
				err = errors.New("UCI не подтвердил запись")
			}
		}
		if err == nil && apply {
			err = applyNetwork()
		}
	}
	if err != nil {
		if e := persistNetwork(&old); e != nil {
			return old, errors.New("Сбой записи и восстановления UCI")
		}
		if apply {
			if e := applyNetwork(); e != nil {
				return old, errors.New("Не удалось восстановить сетевые службы")
			}
		}
		if n.storedHash != "" && n.storedHash != old.storedHash {
			os.Remove(routeListFile + "." + n.storedHash)
		}
		return old, errors.New("Правила не применены. Восстановлены предыдущие настройки")
	}
	if old.storedHash != "" && old.storedHash != saved.storedHash {
		os.Remove(routeListFile + "." + old.storedHash)
	}
	return saved, nil
}
