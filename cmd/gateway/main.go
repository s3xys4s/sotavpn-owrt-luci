// A headless lab controller. Production platform admission is intentionally separate.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"sota-gateway/internal/config"
	"sota-gateway/internal/core"
)

const corePath = "/usr/libexec/sota-connect/kittend"

type report struct {
	Controller bool   `json:"controller_alive"`
	Core       bool   `json:"core_responding"`
	Process    bool   `json:"core_process_alive"`
	Provider   string `json:"provider_connection"`
	State      *int   `json:"core_state"`
	Traffic    string `json:"traffic_verification"`
	Problem    string `json:"problem,omitempty"`
}

func safeDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return errors.New("cannot create private directory")
	}
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errors.New("private directory must be owned by service UID and mode 0700")
	}
	return nil
}

// This candidate admits only the specific isolated forwarding policy exercised
// on ARM64. A RouterOS/OpenWrt deployment needs its own validated admission.
func labGuard() error {
	cmd := exec.Command("nft", "-nn", "list", "chain", "inet", "sota_test", "forward")
	out, e := cmd.Output()
	if e != nil {
		return errors.New("tested transit firewall unavailable")
	}
	normalized := strings.Join(strings.Fields(string(out)), " ")
	counters := regexp.MustCompile(`counter packets [0-9]+ bytes [0-9]+`)
	// nft --numeric prints NFPROTO_IPV4 as 2 and ESTABLISHED,RELATED as 0x2,0x4.
	expected := `table inet sota_test { chain forward { type filter hook forward priority -10; policy drop; iifname "lan" oifname "sotatest0" meta nfproto 2 counter accept iifname "sotatest0" oifname "lan" ct state 0x2,0x4 counter accept counter } }`
	if counters.ReplaceAllString(normalized, "counter") != expected {
		return errors.New("transit firewall differs from tested policy")
	}
	return nil
}

func activate(c config.Config, a core.Adapter) error {
	profile, e := c.Profile()
	if e != nil {
		return e
	}
	if e = labGuard(); e != nil {
		return e
	}
	return a.Start(profile)
}

func serve(c config.Config) error {
	syscall.Umask(0077)
	if c.ProfileFile == "" {
		c.ProfileFile = filepath.Join(c.StateDir, "provider.json")
	}
	if e := safeDir(c.StateDir); e != nil {
		return e
	}
	run := filepath.Join(c.StateDir, "run")
	if e := safeDir(run); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(run, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return errors.New("cannot open controller lock")
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errors.New("controller already running")
	}
	control := filepath.Join(run, "control.sock")
	endpoint := filepath.Join(run, "core.sock")
	for _, p := range []string{control, endpoint} {
		if st, err := os.Lstat(p); err == nil {
			if st.Mode()&os.ModeSocket == 0 {
				return errors.New("socket path occupied")
			}
			if os.Remove(p) != nil {
				return errors.New("cannot clear stale socket")
			}
		} else if !os.IsNotExist(err) {
			return errors.New("cannot inspect socket")
		}
	}
	listener, e := net.Listen("unix", control)
	if e != nil {
		return errors.New("local controller socket unavailable")
	}
	defer listener.Close()
	state := filepath.Join(c.StateDir, "core")
	if e = safeDir(state); e != nil {
		return e
	}
	a := core.Adapter{Endpoint: endpoint}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(stop)
	clients := make(chan net.Conn)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case clients <- conn:
			case <-done:
				conn.Close()
				return
			}
		}
	}()
	var child *exec.Cmd
	var ended <-chan error
	var retry <-chan time.Time
	var startup <-chan time.Time
	attempts := 0
	wanted := c.Auto
	problem := ""
	spawn := func() {
		attempts++
		child = exec.Command(corePath, "--endpoint", endpoint, "--state-dir", state)
		child.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + state}
		child.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		if child.Start() != nil {
			child = nil
			problem = "core executable unavailable"
			return
		}
		cmd := child
		wait := make(chan error, 1)
		ended = wait
		go func() { wait <- cmd.Wait() }()
		startup = time.After(100 * time.Millisecond)
	}
	if c.Auto {
		if e = labGuard(); e != nil {
			return e
		}
	}
	spawn()
	deadline := time.Now().Add(8 * time.Second)
	for {
		select {
		case <-stop:
			if child != nil {
				_ = a.Stop()
				_ = child.Process.Signal(syscall.SIGTERM)
				select {
				case <-ended:
				case <-time.After(4 * time.Second):
					_ = child.Process.Kill()
					<-ended
				}
			}
			return nil
		case <-ended:
			child = nil
			ended = nil
			startup = nil
			problem = "core exited"
			if attempts < 4 {
				retry = time.After(time.Duration(1<<(attempts-1)) * time.Second)
			} else {
				problem = "core restart limit reached"
			}
		case <-retry:
			retry = nil
			deadline = time.Now().Add(8 * time.Second)
			spawn()
		case <-startup:
			startup = nil
			_, err := a.Status()
			if err != nil {
				if err == core.ErrIPC && time.Now().Before(deadline) {
					startup = time.After(100 * time.Millisecond)
				} else {
					problem = "core handshake failed"
					wanted = false
				}
			} else if wanted {
				if err = activate(c, a); err != nil {
					wanted = false
					problem = err.Error()
				} else {
					problem = ""
				}
			} else {
				problem = ""
			}
		case conn := <-clients:
			conn.SetDeadline(time.Now().Add(15 * time.Second))
			command, err := bufio.NewReaderSize(conn, 64).ReadSlice('\n')
			var requestErr error
			if err != nil {
				requestErr = errors.New("invalid command")
			} else {
				switch strings.TrimSpace(string(command)) {
				case "status", "diagnostics":
				case "connect":
					if child == nil {
						requestErr = errors.New("core unavailable")
					} else {
						requestErr = activate(c, a)
					}
					wanted = requestErr == nil
					if requestErr != nil {
						problem = requestErr.Error()
					} else {
						problem = ""
					}
				case "disconnect":
					wanted = false
					requestErr = a.Stop()
					if requestErr == nil {
						problem = ""
					}
				default:
					requestErr = errors.New("unknown command")
				}
			}
			s, se := a.Status()
			r := report{Controller: true, Process: child != nil, Core: se == nil, State: s.State, Provider: "unverified", Traffic: "unverified", Problem: problem}
			if requestErr != nil {
				r.Problem = requestErr.Error()
			}
			json.NewEncoder(conn).Encode(r)
			conn.Close()
		}
	}
}

func mainErr() error {
	if len(os.Args) != 2 {
		return errors.New("usage: gateway run|check|status|diagnostics|connect|disconnect|version")
	}
	command := os.Args[1]
	if command == "version" {
		fmt.Println("sota-gateway lab, kittend IPC v1")
		return nil
	}
	c, e := config.Load()
	if e != nil {
		return e
	}
	switch command {
	case "check":
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"valid": true, "sources": c.Sources})
	case "run":
		return serve(c)
	case "status", "diagnostics", "connect", "disconnect":
		conn, err := net.DialTimeout("unix", filepath.Join(c.StateDir, "run", "control.sock"), 2*time.Second)
		if err != nil {
			return errors.New("controller unavailable")
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(35 * time.Second))
		if _, err = fmt.Fprintln(conn, command); err != nil {
			return errors.New("controller write failed")
		}
		var r report
		if json.NewDecoder(conn).Decode(&r) != nil {
			return errors.New("invalid controller response")
		}
		if err = json.NewEncoder(os.Stdout).Encode(r); err != nil {
			return err
		}
		if r.Problem != "" && (command == "connect" || command == "disconnect") {
			return errors.New("operation failed")
		}
		return nil
	default:
		return errors.New("unknown command")
	}
}

func main() {
	if e := mainErr(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
