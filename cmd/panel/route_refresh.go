package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"
)

func (r *routerControl) scheduleRefresh(minutes int) {
	// Callers hold app.mu. Coalesce successive edits to the interval.
	select {
	case <-r.schedule:
	default:
	}
	r.schedule <- time.Duration(minutes) * time.Minute
}

func (a *app) runListRefresh(ctx context.Context) {
	// Use the saved rules immediately at boot and check their sources shortly after.
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case delay := <-a.router.schedule:
			timer.Reset(delay)
		case <-timer.C:
			timer.Reset(a.refreshLists(ctx))
		}
	}
}

// A failed source keeps its last valid copy while other sources can advance.
func refreshSources(ctx context.Context, client *http.Client, old []sourceList) ([]sourceList, []error) {
	next := slices.Clone(old)
	total := 0
	for _, list := range old {
		total += len(list.Text)
	}
	var failures []error
	for i, list := range old {
		if ctx.Err() != nil {
			break
		}
		body, err := downloadList(ctx, client, list.URL)
		size := total - len(list.Text) + len(body)
		if err == nil && size > maxURLTotal {
			err = fmt.Errorf("Общий размер загруженных списков превышает 10 МиБ")
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("Список %d: %w", i+1, err))
			continue
		}
		next[i].Text = body
		total = size
	}
	return next, failures
}

func (a *app) refreshLists(ctx context.Context) time.Duration {
	a.mu.Lock()
	snapshot, err := getNetwork()
	if err != nil {
		a.record("warn", "Автообновление списков: "+err.Error())
	}
	a.mu.Unlock()
	if err != nil {
		return time.Minute
	}
	delay := time.Duration(snapshot.RefreshMinutes) * time.Minute
	if len(snapshot.document.Lists)+len(snapshot.document.Exceptions.Lists) == 0 {
		return delay
	}

	// Downloads run outside the RPC lock so even an offline host cannot block
	// status or edits. Discard the result if the configuration changed meanwhile.
	lists, failures := refreshSources(ctx, listClient, snapshot.document.Lists)
	exclusions, exFailures := refreshSources(ctx, listClient, snapshot.document.Exceptions.Lists)
	if ctx.Err() != nil {
		return delay
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, err := getNetwork()
	if err != nil {
		a.record("warn", "Автообновление списков: "+err.Error())
		return time.Minute
	}
	if current.Revision != snapshot.Revision || current.storedHash != snapshot.storedHash {
		return time.Duration(current.RefreshMinutes) * time.Minute
	}
	for _, failure := range failures {
		a.record("warn", "Автообновление списков: "+failure.Error())
	}
	for _, failure := range exFailures {
		a.record("warn", "Автообновление списков: Исключения: "+failure.Error())
	}
	if !slices.Equal(lists, current.document.Lists) || !slices.Equal(exclusions, current.document.Exceptions.Lists) {
		next := current
		next.document.Lists = lists
		next.document.Exceptions.Lists = exclusions
		if _, err := commitNetwork(current, next, current.Enabled); err != nil {
			a.record("error", "Автообновление списков: "+err.Error())
		} else {
			a.record("info", "Списки по ссылкам обновлены")
		}
	}
	return delay
}
