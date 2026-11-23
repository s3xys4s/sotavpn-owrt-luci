// Package core owns the observed kittend v1 Unix socket contract.
package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"time"
)

const Protocol = 1

var ErrIPC = errors.New("core IPC failed")
var ErrIncompatible = errors.New("unsupported core protocol")
var ErrRejected = errors.New("core rejected operation")

type Adapter struct{ Endpoint, Interface string }
type session struct {
	net.Conn
	reader *bufio.Reader
	id     int
}
type Status struct {
	State *int  `json:"state"`
	Since int64 `json:"connected_since"`
}

func (s *session) call(method string, params any, out any) error {
	s.id++
	if err := s.SetDeadline(time.Now().Add(12 * time.Second)); err != nil {
		return ErrIPC
	}
	req := struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{s.id, method, params}
	if json.NewEncoder(s.Conn).Encode(req) != nil {
		return ErrIPC
	}
	for i := 0; i < 256; i++ {
		frame, err := s.reader.ReadSlice('\n')
		if err != nil {
			return ErrIPC
		}
		var reply struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(frame, &reply) != nil {
			return ErrIPC
		}
		if reply.ID == nil {
			continue
		} // asynchronous events, potentially sensitive
		if *reply.ID != s.id {
			return ErrIPC
		}
		if len(reply.Error) > 0 && string(reply.Error) != "null" {
			return ErrRejected
		}
		if len(reply.Result) == 0 || string(reply.Result) == "null" {
			return ErrIPC
		}
		if out != nil && json.Unmarshal(reply.Result, out) != nil {
			return ErrIPC
		}
		return nil
	}
	return ErrIPC
}

func (a Adapter) open() (*session, error) {
	c, err := net.DialTimeout("unix", a.Endpoint, 2*time.Second)
	if err != nil {
		return nil, ErrIPC
	}
	s := &session{Conn: c, reader: bufio.NewReaderSize(c, 1<<20)}
	var hello struct {
		Version int `json:"protocolVersion"`
	}
	if err = s.call("hello", map[string]int{"protocolVersion": Protocol}, &hello); err != nil || hello.Version != Protocol {
		c.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrIncompatible
	}
	return s, nil
}

func (a Adapter) Status() (Status, error) {
	var status Status
	s, err := a.open()
	if err != nil {
		return status, err
	}
	defer s.Close()
	err = s.call("status", nil, &status)
	if err == nil && status.State == nil {
		err = ErrIPC
	}
	return status, err
}

func (a Adapter) Start(profile []byte) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	defer s.Close()
	// gVisor + TUN is the mode exercised by the forwarded-client fixture.
	name := a.Interface
	if name == "" {
		name = "sotatest0"
	}
	options, err := json.Marshal(map[string]any{"inbound_mode": "tun", "interface_name": name, "network_stack": "gvisor", "ipv6": false, "bypass_lan": false})
	if err != nil {
		return err
	}
	return s.call("start", map[string]string{"configuration": string(profile), "localOptionsJson": string(options)}, nil)
}

func (a Adapter) Stop() error {
	s, err := a.open()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.call("stop", nil, nil)
}
