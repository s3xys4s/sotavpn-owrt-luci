package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

func serviceStopping() bool {
	_, err := os.Stat(routeRuntime + "stopping")
	return err == nil
}

func (a *app) wantsTunnel() bool { return a.cfg.WantTunnel != nil && *a.cfg.WantTunnel }

func (a *app) setWanted(on bool) error {
	if a.cfg.WantTunnel != nil && a.wantsTunnel() == on {
		return nil
	}
	next := a.cfg
	next.WantTunnel = &on
	return a.save(next)
}

func setRouterPolicy(on bool) (bool, error) {
	old, err := getNetwork()
	if err != nil {
		return false, err
	}
	if old.Enabled == on {
		if !on {
			return false, policy("clear")
		}
		if on && policy("check") != nil {
			return false, applyNetwork()
		}
		return false, nil
	}
	next := old
	next.Enabled = on
	_, err = commitNetwork(old, next, true)
	return err == nil, err
}

func (a *app) stopRouter(ctx context.Context, suspend bool) (any, error) {
	if !suspend {
		if err := a.setWanted(false); err != nil {
			return nil, err
		}
	}
	localErr := a.stopLocal()
	var policyErr error
	if suspend {
		policyErr = policy("clear")
	} else {
		_, policyErr = setRouterPolicy(false)
	}
	// Restore LAN before contacting the provider, even if WAN is unavailable.
	remoteErr := a.closeProvider(ctx)
	if remoteErr != nil {
		a.record("warn", "Не удалось закрыть сессию Sota")
	}
	return true, errors.Join(localErr, policyErr, remoteErr)
}

func (a *app) stopLocal() error {
	st, err := a.router.core.Status()
	if _, linkErr := net.InterfaceByName("sota0"); linkErr != nil && (err != nil || st.State != nil && *st.State == 0) {
		return nil
	}
	_, err = a.control("disconnect", nil)
	return err
}

func (a *app) maintainTunnel(ctx context.Context) {
	delay := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		a.mu.Lock()
		err := a.recoverTunnel(ctx)
		if err != nil {
			a.record("warn", "Повторное подключение: "+err.Error())
		}
		a.mu.Unlock()
		if err != nil {
			delay = min(60*time.Second, max(4*time.Second, delay*2))
		} else {
			delay = 3 * time.Second
		}
	}
}

func (a *app) recoverTunnel(ctx context.Context) error {
	if !a.wantsTunnel() || serviceStopping() {
		return nil
	}
	st, err := a.router.core.Status()
	if err != nil || st.State == nil {
		return nil
	} // procd supervises the core process.
	_, linkErr := net.InterfaceByName("sota0")
	if *st.State == 2 && linkErr == nil {
		return nil
	}
	if *st.State != 0 && *st.State != 4 && !(*st.State == 2 && linkErr != nil) {
		return nil
	}
	enabled, err := exec.Command("uci", "-q", "get", "sota.main.enabled").Output()
	if err != nil || strings.TrimSpace(string(enabled)) != "1" {
		return err
	}
	if *st.State != 0 {
		if err := a.stopLocal(); err != nil {
			return err
		}
	}
	q := rpcRequest{Method: "tunnel.connect"}
	q.Params.ID, q.Params.Protocol = a.cfg.Selected, a.cfg.Protocol
	_, err = a.callLocked(ctx, q)
	return err
}
