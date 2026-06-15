package app

import (
	"context"
	"sync"
	"time"

	"r1rpc/internal/store"
)

type ProbeBucket struct {
	Minute   int64  `json:"-"`
	Label    string `json:"t"`
	Online   int    `json:"online"`
	Healthy  int    `json:"healthy"`
	Total    int    `json:"total"`
	MaxLatMs int64  `json:"maxLatMs"`
}

type probeHistory struct {
	mu      sync.Mutex
	buckets map[string]map[int64]*ProbeBucket // group -> minute_ts -> bucket
	store   *store.Store
}

func newProbeHistory(st *store.Store) *probeHistory {
	ph := &probeHistory{
		buckets: map[string]map[int64]*ProbeBucket{},
		store:   st,
	}
	ph.loadFromDB()
	return ph
}

func (ph *probeHistory) loadFromDB() {
	if ph.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := ph.store.LoadProbeBuckets(ctx, 65)
	if err != nil {
		return
	}
	ph.mu.Lock()
	defer ph.mu.Unlock()
	for _, r := range rows {
		gm := ph.buckets[r.GroupName]
		if gm == nil {
			gm = map[int64]*ProbeBucket{}
			ph.buckets[r.GroupName] = gm
		}
		gm[r.MinuteTS] = &ProbeBucket{
			Minute:   r.MinuteTS,
			Label:    time.Unix(r.MinuteTS, 0).Format("15:04"),
			Online:   r.Online,
			Healthy:  r.Healthy,
			Total:    r.Total,
			MaxLatMs: r.MaxLatMs,
		}
	}
}

func (ph *probeHistory) Record(group string, online int, ok bool, latencyMs int64) {
	now := time.Now().Truncate(time.Minute).Unix()
	ph.mu.Lock()
	gm := ph.buckets[group]
	if gm == nil {
		gm = map[int64]*ProbeBucket{}
		ph.buckets[group] = gm
	}
	b := gm[now]
	if b == nil {
		b = &ProbeBucket{Minute: now, Label: time.Unix(now, 0).Format("15:04"), Online: online}
		gm[now] = b
	}
	if online > b.Online {
		b.Online = online
	}
	b.Total++
	if ok {
		b.Healthy++
	}
	if latencyMs > b.MaxLatMs {
		b.MaxLatMs = latencyMs
	}
	ph.mu.Unlock()

	if ph.store != nil {
		h := 0
		if ok {
			h = 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = ph.store.UpsertProbeBucket(ctx, store.ProbeBucketRow{
			GroupName: group, MinuteTS: now, Online: online, Healthy: h, Total: 1, MaxLatMs: latencyMs,
		})
		cancel()
	}
}

func (ph *probeHistory) RecordOnline(group string, online int) {
	now := time.Now().Truncate(time.Minute).Unix()
	ph.mu.Lock()
	gm := ph.buckets[group]
	if gm == nil {
		gm = map[int64]*ProbeBucket{}
		ph.buckets[group] = gm
	}
	b := gm[now]
	if b == nil {
		b = &ProbeBucket{Minute: now, Label: time.Unix(now, 0).Format("15:04")}
		gm[now] = b
	}
	if online > b.Online {
		b.Online = online
	}
	ph.mu.Unlock()

	if ph.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = ph.store.UpsertProbeBucketOnline(ctx, group, now, online)
		cancel()
	}
}

func (ph *probeHistory) Last60(group string) []ProbeBucket {
	end := time.Now().Truncate(time.Minute).Unix()
	ph.mu.Lock()
	gm := ph.buckets[group]
	out := make([]ProbeBucket, 60)
	for i := 59; i >= 0; i-- {
		ts := end - int64(i)*60
		if b, ok := gm[ts]; ok {
			out[59-i] = *b
		} else {
			out[59-i] = ProbeBucket{Minute: ts, Label: time.Unix(ts, 0).Format("15:04")}
		}
	}
	ph.mu.Unlock()
	return out
}

func (ph *probeHistory) Cleanup() {
	cutoff := time.Now().Add(-65 * time.Minute).Truncate(time.Minute).Unix()
	ph.mu.Lock()
	for group, gm := range ph.buckets {
		for ts := range gm {
			if ts < cutoff {
				delete(gm, ts)
			}
		}
		if len(gm) == 0 {
			delete(ph.buckets, group)
		}
	}
	ph.mu.Unlock()

	if ph.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = ph.store.CleanupProbeBuckets(ctx, 65)
		cancel()
	}
}
