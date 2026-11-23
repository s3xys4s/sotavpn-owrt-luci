package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const rpcChunk = 12 * 1024

type routeUpload struct {
	data    []byte
	expires time.Time
}

// LuCI's HTTP/ubus transport has a smaller message limit than the rule editor.
func (a *app) readRoutePart(q rpcRequest) (any, error) {
	n, err := getNetwork()
	if err != nil {
		return nil, err
	}
	if q.Params.Revision != "" && q.Params.Revision != n.Revision {
		return nil, errors.New("Настройки изменились во время загрузки")
	}
	data, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	start := q.Params.Offset
	if start < 0 || start > len(data) {
		return nil, errors.New("Некорректная позиция чтения")
	}
	return map[string]any{"revision": n.Revision, "size": len(data), "chunk": data[start:min(start+rpcChunk, len(data))]}, nil
}
func (a *app) writeRoutePart(ctx context.Context, q rpcRequest) (any, error) {
	piece, err := base64.StdEncoding.DecodeString(q.Params.Chunk)
	if err != nil || len(piece) == 0 || len(piece) > rpcChunk {
		return nil, errors.New("Некорректная часть списка")
	}
	now := time.Now()
	if a.router.uploads == nil {
		a.router.uploads = make(map[string]*routeUpload)
	}
	for id, u := range a.router.uploads {
		if now.After(u.expires) {
			delete(a.router.uploads, id)
		}
	}
	token := q.Params.Transfer
	u := a.router.uploads[token]
	if token == "" && q.Params.Offset == 0 {
		if len(a.router.uploads) >= 4 {
			return nil, errors.New("Дождитесь завершения предыдущих загрузок")
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		token = hex.EncodeToString(id[:])
		u = &routeUpload{}
		a.router.uploads[token] = u
	}
	if u == nil || q.Params.Offset != len(u.data) {
		return nil, errors.New("Повторите загрузку списка")
	}
	if len(u.data)+len(piece) > 16<<20 {
		delete(a.router.uploads, token)
		return nil, errors.New("Настройки превышают 16 МиБ")
	}
	u.data = append(u.data, piece...)
	u.expires = now.Add(3 * time.Minute)
	if !q.Params.Final {
		return map[string]string{"transfer": token}, nil
	}
	delete(a.router.uploads, token)
	var n networkSettings
	if err = json.Unmarshal(u.data, &n); err != nil {
		return nil, errors.New("Не удалось прочитать настройки")
	}
	_, err = a.setNetwork(ctx, n)
	a.logAction("network.set", err)
	return err == nil, err
}
